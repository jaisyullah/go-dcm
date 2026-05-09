package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"go-dcm/model"
	"go-dcm/service"
)

// OrthancCfg is the global Orthanc configuration, loaded at startup.
var OrthancCfg service.OrthancConfig

// SendToOrthancRequest represents the parsed form data for the send-to-orthanc endpoint.
type SendToOrthancRequest struct {
	FileType      string                     // "img", "pdf", "cda", "stl"
	ImgParams     *Img2DcmRequest            // populated when FileType == "img"
	PdfParams     *Pdf2DcmRequest            // populated when FileType == "pdf"
	CdaParams     *Cda2DcmRequest            // populated when FileType == "cda"
	StlParams     *Stl2DcmRequest            // populated when FileType == "stl"
	OrthancModify *service.OrthancModifyRequest
}

// SendToOrthancResponse is the JSON response returned to the caller.
type SendToOrthancResponse struct {
	Status string                        `json:"status"`
	Upload *service.OrthancUploadResponse `json:"upload"`
	Modify json.RawMessage               `json:"modify"`
}

// supportedFileTypes lists the valid filetype parameter values.
var supportedFileTypes = map[string]bool{
	"img": true,
	"pdf": true,
	"cda": true,
	"stl": true,
}

// HandleSendToOrthanc handles POST /api/v1/send-to-orthanc
//
// Workflow:
//  1. Convert uploaded file to DICOM using the appropriate DCMTK tool
//  2. Upload the DICOM to Orthanc (POST /instances)
//  3. Modify study tags (POST /studies/{id}/modify)
//  4. On modify failure, rollback by deleting the uploaded instance
func HandleSendToOrthanc(w http.ResponseWriter, r *http.Request) {
	// Verify Orthanc is configured
	if !OrthancCfg.IsConfigured() {
		model.WriteError(w, http.StatusServiceUnavailable, "ORTHANC_NOT_CONFIGURED",
			"Orthanc is not configured. Set ORTHANC_URL environment variable.", "")
		return
	}

	// Use the largest upload limit across all types
	maxSize := MaxImageUploadSize
	if MaxPDFUploadSize > maxSize {
		maxSize = MaxPDFUploadSize
	}
	if MaxCDAUploadSize > maxSize {
		maxSize = MaxCDAUploadSize
	}
	if MaxSTLUploadSize > maxSize {
		maxSize = MaxSTLUploadSize
	}

	if err := r.ParseMultipartForm(maxSize); err != nil {
		model.WriteError(w, http.StatusBadRequest, "INVALID_FORM", "Failed to parse multipart form", err.Error())
		return
	}

	// Validate filetype parameter
	fileType := strings.TrimSpace(strings.ToLower(r.FormValue("filetype")))
	if fileType == "" {
		model.WriteError(w, http.StatusBadRequest, "MISSING_FILETYPE",
			"Missing 'filetype' parameter. Must be one of: img, pdf, cda, stl", "")
		return
	}
	if !supportedFileTypes[fileType] {
		model.WriteError(w, http.StatusBadRequest, "INVALID_FILETYPE",
			"Invalid 'filetype' parameter. Must be one of: img, pdf, cda, stl", "got: "+fileType)
		return
	}

	// Get uploaded file
	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		model.WriteError(w, http.StatusBadRequest, "MISSING_FILE", "Missing 'file' field in form data", "")
		return
	}
	defer file.Close()

	// Parse orthanc_modify payload (required)
	modifyStr := r.FormValue("orthanc_modify")
	if modifyStr == "" {
		model.WriteError(w, http.StatusBadRequest, "MISSING_ORTHANC_MODIFY",
			"Missing 'orthanc_modify' parameter with Orthanc modify payload", "")
		return
	}
	var modifyReq service.OrthancModifyRequest
	if err := json.Unmarshal([]byte(modifyStr), &modifyReq); err != nil {
		model.WriteError(w, http.StatusBadRequest, "INVALID_ORTHANC_MODIFY",
			"Invalid 'orthanc_modify' JSON", err.Error())
		return
	}

	// Parse optional conversion parameters
	paramsStr := r.FormValue("parameters")

	// Create temp directory for the conversion
	tempDir, err := os.MkdirTemp("", "send_to_orthanc_*")
	if err != nil {
		slog.Error("failed to create temp directory", "error", err)
		model.WriteError(w, http.StatusInternalServerError, "TEMP_DIR_ERROR",
			"Failed to create temporary directory", "")
		return
	}
	defer os.RemoveAll(tempDir)

	// Save uploaded file
	inputFilePath := filepath.Join(tempDir, service.SanitizeFilename(fileHeader.Filename))
	out, err := os.Create(inputFilePath)
	if err != nil {
		slog.Error("failed to create temp file", "error", err)
		model.WriteError(w, http.StatusInternalServerError, "FILE_SAVE_ERROR",
			"Failed to save uploaded file", "")
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		slog.Error("failed to write uploaded file", "error", err)
		model.WriteError(w, http.StatusInternalServerError, "FILE_WRITE_ERROR",
			"Failed to write uploaded file", "")
		return
	}
	out.Close()

	outputFilePath := filepath.Join(tempDir, "output.dcm")

	// ── Step 1: Convert to DICOM ────────────────────────────────────────
	if err := convertToDICOM(fileType, inputFilePath, outputFilePath, tempDir, fileHeader.Filename, paramsStr); err != nil {
		model.WriteError(w, http.StatusInternalServerError, "CONVERSION_FAILED",
			"DICOM conversion failed", err.Error())
		return
	}

	// ── Step 2: Upload to Orthanc ───────────────────────────────────────
	uploadResp, err := service.UploadInstance(&OrthancCfg, outputFilePath)
	if err != nil {
		model.WriteError(w, http.StatusBadGateway, "ORTHANC_UPLOAD_FAILED",
			"Failed to upload DICOM to Orthanc", err.Error())
		return
	}

	// ── Step 3: Modify study tags ───────────────────────────────────────
	modifyResp, err := service.ModifyStudy(&OrthancCfg, uploadResp.ParentStudy, &modifyReq)
	if err != nil {
		slog.Error("modify failed, rolling back upload",
			"instance_id", uploadResp.ID,
			"study_id", uploadResp.ParentStudy,
			"error", err,
		)

		// Rollback: delete the uploaded instance
		if delErr := service.DeleteInstance(&OrthancCfg, uploadResp.ID); delErr != nil {
			slog.Error("rollback failed: could not delete instance",
				"instance_id", uploadResp.ID,
				"error", delErr,
			)
		}

		model.WriteError(w, http.StatusBadGateway, "ORTHANC_MODIFY_FAILED",
			"Failed to modify study tags in Orthanc (upload was rolled back)", err.Error())
		return
	}

	// ── Step 4: Return combined response ────────────────────────────────
	model.WriteJSON(w, http.StatusOK, SendToOrthancResponse{
		Status: "success",
		Upload: uploadResp,
		Modify: modifyResp,
	})
}

// convertToDICOM runs the appropriate DCMTK tool based on filetype.
func convertToDICOM(fileType, inputFilePath, outputFilePath, tempDir, originalFilename, paramsStr string) error {
	switch fileType {
	case "img":
		return convertImg(inputFilePath, outputFilePath, tempDir, originalFilename, paramsStr)
	case "pdf":
		return convertPdf(inputFilePath, outputFilePath, paramsStr)
	case "cda":
		return convertCda(inputFilePath, outputFilePath, paramsStr)
	case "stl":
		return convertStl(inputFilePath, outputFilePath, paramsStr)
	default:
		return fmt.Errorf("unsupported filetype: %s", fileType)
	}
}

func convertImg(inputFilePath, outputFilePath, tempDir, originalFilename, paramsStr string) error {
	var reqBody Img2DcmRequest
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &reqBody); err != nil {
			return fmt.Errorf("invalid img parameters JSON: %w", err)
		}
	}

	if err := service.ValidateKeys(reqBody.Keys); err != nil {
		return fmt.Errorf("invalid key format: %w", err)
	}

	// Validate file extension
	if err := service.ValidateFileExtension(originalFilename, service.AllowedImageExtensions); err != nil {
		return fmt.Errorf("invalid image file type: %w", err)
	}

	// PNG → BMP conversion
	if service.IsPNG(originalFilename) {
		slog.Info("converting PNG to BMP for DCMTK compatibility", "file", originalFilename)
		bmpPath, err := service.ConvertPNGtoBMP(inputFilePath, tempDir)
		if err != nil {
			return fmt.Errorf("PNG to BMP conversion failed: %w", err)
		}
		inputFilePath = bmpPath
		reqBody.InputFormat = "BMP"
	}

	args := reqBody.ToArgs()
	return service.RunDCMTK("img2dcm", inputFilePath, outputFilePath, args)
}

func convertPdf(inputFilePath, outputFilePath, paramsStr string) error {
	var reqBody Pdf2DcmRequest
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &reqBody); err != nil {
			return fmt.Errorf("invalid pdf parameters JSON: %w", err)
		}
	}

	if err := service.ValidateKeys(reqBody.Keys); err != nil {
		return fmt.Errorf("invalid key format: %w", err)
	}

	// Validate MIME type
	if err := service.ValidateMIMEType(inputFilePath, []string{"application/pdf"}); err != nil {
		return fmt.Errorf("invalid PDF file: %w", err)
	}

	args := reqBody.ToArgs()
	return service.RunDCMTK("pdf2dcm", inputFilePath, outputFilePath, args)
}

func convertCda(inputFilePath, outputFilePath, paramsStr string) error {
	var reqBody Cda2DcmRequest
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &reqBody); err != nil {
			return fmt.Errorf("invalid cda parameters JSON: %w", err)
		}
	}

	if err := service.ValidateKeys(reqBody.Keys); err != nil {
		return fmt.Errorf("invalid key format: %w", err)
	}

	args := reqBody.ToArgs()
	return service.RunDCMTK("cda2dcm", inputFilePath, outputFilePath, args)
}

func convertStl(inputFilePath, outputFilePath, paramsStr string) error {
	var reqBody Stl2DcmRequest
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &reqBody); err != nil {
			return fmt.Errorf("invalid stl parameters JSON: %w", err)
		}
	}

	if err := service.ValidateKeys(reqBody.Keys); err != nil {
		return fmt.Errorf("invalid key format: %w", err)
	}

	args := reqBody.ToArgs()
	return service.RunDCMTK("stl2dcm", inputFilePath, outputFilePath, args)
}
