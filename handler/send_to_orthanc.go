package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"dicom-converter-api/model"
	"dicom-converter-api/service"
)

// OrthancCfg is the global Orthanc configuration, loaded at startup.
var OrthancCfg service.OrthancConfig

// SendToOrthancResponse is the JSON response returned to the caller for async status.
type SendToOrthancResponse struct {
	Status string `json:"status"`
	JobID  string `json:"job_id"`
}

// SendToOrthancResult is the payload stored in the job result field on success.
type SendToOrthancResult struct {
	Upload *service.OrthancUploadResponse `json:"upload"`
	Modify json.RawMessage                `json:"modify"`
}

// supportedFileTypes lists the valid filetype parameter values.
var supportedFileTypes = map[string]bool{
	"img": true,
	"pdf": true,
	"cda": true,
	"stl": true,
}

// HandleSendToOrthanc handles POST /api/v1/send-to-orthanc
// Now refactored to be ASYNCHRONOUS.
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
	if fileType == "" || !supportedFileTypes[fileType] {
		model.WriteError(w, http.StatusBadRequest, "INVALID_FILETYPE",
			"Invalid 'filetype' parameter. Must be one of: img, pdf, cda, stl", "")
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
			"Missing 'orthanc_modify' parameter", "")
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

	// 1. Prepare temp storage immediately (while we have the file handle)
	tempDir, err := os.MkdirTemp("", "send_to_orthanc_*")
	if err != nil {
		slog.Error("failed to create temp directory", "error", err)
		model.WriteError(w, http.StatusInternalServerError, "TEMP_DIR_ERROR", "Failed to create temporary directory", "")
		return
	}

	inputFilePath := filepath.Join(tempDir, service.SanitizeFilename(fileHeader.Filename))
	out, err := os.Create(inputFilePath)
	if err != nil {
		os.RemoveAll(tempDir)
		slog.Error("failed to create temp file", "error", err)
		model.WriteError(w, http.StatusInternalServerError, "FILE_SAVE_ERROR", "Failed to save uploaded file", "")
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		os.RemoveAll(tempDir)
		slog.Error("failed to write uploaded file", "error", err)
		model.WriteError(w, http.StatusInternalServerError, "FILE_WRITE_ERROR", "Failed to write uploaded file", "")
		return
	}
	out.Close()

	// 2. Create Background Job
	jobID := service.CreateJob()

	// 3. Enqueue Task
	service.EnqueueTask(service.Task{
		JobID: jobID,
		ExecuteFunc: func(ctx context.Context) (any, error) {
			// Cleanup files when the job finishes (even if it fails)
			defer os.RemoveAll(tempDir)

			// Extract demographics for pre-emptive alignment
			var patientID, patientName, patientBirthDate, patientSex string
			if modifyReq.Replace != nil {
				if pid, ok := modifyReq.Replace["PatientID"].(string); ok {
					patientID = pid
				}
				if name, ok := modifyReq.Replace["PatientName"].(string); ok {
					patientName = name
				}
				if dob, ok := modifyReq.Replace["PatientBirthDate"].(string); ok {
					patientBirthDate = dob
				}
				if sex, ok := modifyReq.Replace["PatientSex"].(string); ok {
					patientSex = sex
				}
			}

			// Pre-emptively align patient demographics in Orthanc if they mismatch
			if patientID != "" {
				if err := service.PreemptiveAlignPatientDemographics(&OrthancCfg, patientID, patientName, patientBirthDate, patientSex); err != nil {
					return nil, fmt.Errorf("demographic alignment failed: %w", err)
				}
			}

			outputFilePath := filepath.Join(tempDir, "output.dcm")

			// Step 1: Convert to DICOM
			if err := convertToDICOM(ctx, fileType, inputFilePath, outputFilePath, tempDir, fileHeader.Filename, paramsStr); err != nil {
				return nil, fmt.Errorf("conversion failed: %w", err)
			}

			// Step 2: Upload to Orthanc
			uploadResp, err := service.UploadInstance(&OrthancCfg, outputFilePath)
			if err != nil {
				return nil, fmt.Errorf("orthanc upload failed: %w", err)
			}

			// Step 3: Modify study tags
			// Strip patient demographics from Replace payload because they were already embedded
			// in the DICOM instances during conversion. This avoids triggering demographic mismatch (400)
			// and patient-level modify (500) errors in Orthanc under SQLite locks.
			if modifyReq.Replace != nil {
				delete(modifyReq.Replace, "PatientName")
				delete(modifyReq.Replace, "PatientBirthDate")
				delete(modifyReq.Replace, "PatientSex")
				delete(modifyReq.Replace, "PatientID")
			}

			modifyResp, err := service.ModifyStudy(&OrthancCfg, uploadResp.ParentStudy, &modifyReq)
			if err != nil {
				slog.Error("modify failed, rolling back upload", "job_id", jobID, "instance_id", uploadResp.ID, "error", err)
				_ = service.DeleteInstance(&OrthancCfg, uploadResp.ID)
				return nil, fmt.Errorf("orthanc modify failed: %w", err)
			}

			// Success! Return the combined result
			return SendToOrthancResult{
				Upload: uploadResp,
				Modify: modifyResp,
			}, nil
		},
	})

	// 4. Respond with 202 Accepted and JobID
	model.WriteJSON(w, http.StatusAccepted, SendToOrthancResponse{
		Status: "success",
		JobID:  jobID,
	})
}

// convertToDICOM runs the appropriate DCMTK tool based on filetype.
func convertToDICOM(ctx context.Context, fileType, inputFilePath, outputFilePath, tempDir, originalFilename, paramsStr string) error {
	switch fileType {
	case "img":
		return convertImg(ctx, inputFilePath, outputFilePath, tempDir, originalFilename, paramsStr)
	case "pdf":
		return convertPdf(ctx, inputFilePath, outputFilePath, paramsStr)
	case "cda":
		return convertCda(ctx, inputFilePath, outputFilePath, paramsStr)
	case "stl":
		return convertStl(ctx, inputFilePath, outputFilePath, paramsStr)
	default:
		return fmt.Errorf("unsupported filetype: %s", fileType)
	}
}

func convertImg(ctx context.Context, inputFilePath, outputFilePath, tempDir, originalFilename, paramsStr string) error {
	var reqBody Img2DcmRequest
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &reqBody); err != nil {
			return fmt.Errorf("invalid img parameters JSON: %w", err)
		}
	}

	if err := service.ValidateKeys(reqBody.Keys); err != nil {
		return fmt.Errorf("invalid key format: %w", err)
	}

	if err := service.ValidateFileExtension(originalFilename, service.AllowedImageExtensions); err != nil {
		return fmt.Errorf("invalid image file type: %w", err)
	}

	if service.IsPNG(originalFilename) {
		bmpPath, err := service.ConvertPNGtoBMP(inputFilePath, tempDir)
		if err != nil {
			return fmt.Errorf("PNG to BMP conversion failed: %w", err)
		}
		inputFilePath = bmpPath
		reqBody.InputFormat = "BMP"
	}

	return service.RunDCMTK(ctx, "img2dcm", inputFilePath, outputFilePath, reqBody.ToArgs())
}

func convertPdf(ctx context.Context, inputFilePath, outputFilePath, paramsStr string) error {
	var reqBody Pdf2DcmRequest
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &reqBody); err != nil {
			return fmt.Errorf("invalid pdf parameters JSON: %w", err)
		}
	}
	if err := service.ValidateKeys(reqBody.Keys); err != nil {
		return fmt.Errorf("invalid key format: %w", err)
	}
	if err := service.ValidateMIMEType(inputFilePath, []string{"application/pdf"}); err != nil {
		return fmt.Errorf("invalid PDF file: %w", err)
	}
	return service.RunDCMTK(ctx, "pdf2dcm", inputFilePath, outputFilePath, reqBody.ToArgs())
}

func convertCda(ctx context.Context, inputFilePath, outputFilePath, paramsStr string) error {
	var reqBody Cda2DcmRequest
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &reqBody); err != nil {
			return fmt.Errorf("invalid cda parameters JSON: %w", err)
		}
	}
	if err := service.ValidateKeys(reqBody.Keys); err != nil {
		return fmt.Errorf("invalid key format: %w", err)
	}
	return service.RunDCMTK(ctx, "cda2dcm", inputFilePath, outputFilePath, reqBody.ToArgs())
}

func convertStl(ctx context.Context, inputFilePath, outputFilePath, paramsStr string) error {
	var reqBody Stl2DcmRequest
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &reqBody); err != nil {
			return fmt.Errorf("invalid stl parameters JSON: %w", err)
		}
	}
	if err := service.ValidateKeys(reqBody.Keys); err != nil {
		return fmt.Errorf("invalid key format: %w", err)
	}
	return service.RunDCMTK(ctx, "stl2dcm", inputFilePath, outputFilePath, reqBody.ToArgs())
}
