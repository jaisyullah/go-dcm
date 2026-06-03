package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-dcm/model"
	"go-dcm/service"
)

// SendToOrthancFromURLsRequest represents the JSON request payload for URL-based conversions.
type SendToOrthancFromURLsRequest struct {
	FileType      string                     `json:"filetype"` // "img", "pdf", "cda", "stl"
	URLs          []string                   `json:"urls"`
	Parameters    json.RawMessage            `json:"parameters,omitempty"`
	OrthancModify service.OrthancModifyRequest `json:"orthanc_modify"`
}

// SendToOrthancFromURLsResponse is the response returned to the caller.
type SendToOrthancFromURLsResponse struct {
	Status string                        `json:"status"`
	Upload *service.OrthancUploadResponse `json:"upload"`
	Modify json.RawMessage               `json:"modify"`
}

// HandleSendToOrthancFromURLs handles POST /api/v1/send-to-orthanc-from-urls
func HandleSendToOrthancFromURLs(w http.ResponseWriter, r *http.Request) {
	// Verify Orthanc is configured
	if !OrthancCfg.IsConfigured() {
		model.WriteError(w, http.StatusServiceUnavailable, "ORTHANC_NOT_CONFIGURED",
			"Orthanc is not configured. Set ORTHANC_URL environment variable.", "")
		return
	}

	// Parse JSON request body
	var req SendToOrthancFromURLsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		model.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Failed to parse JSON body", err.Error())
		return
	}

	// Validate filetype parameter
	fileType := strings.TrimSpace(strings.ToLower(req.FileType))
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

	// Validate URLs array
	if len(req.URLs) == 0 {
		model.WriteError(w, http.StatusBadRequest, "MISSING_URLS", "The 'urls' array must contain at least one URL link.", "")
		return
	}

	// Convert parameters raw message to string
	var paramsStr string
	if len(req.Parameters) > 0 {
		paramsStr = string(req.Parameters)
	}

	// Create temp directory for this batch
	tempDir, err := os.MkdirTemp("", "send_to_orthanc_urls_*")
	if err != nil {
		slog.Error("failed to create temp directory", "error", err)
		model.WriteError(w, http.StatusInternalServerError, "TEMP_DIR_ERROR",
			"Failed to create temporary directory", "")
		return
	}
	defer os.RemoveAll(tempDir)

	// Keep track of uploaded instance IDs and the parent study ID
	var uploadedInstanceIDs []string
	var parentStudyID string
	var lastUploadResp *service.OrthancUploadResponse

	// HTTP client with a reasonable timeout for downloading files
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Download, convert, and upload each URL
	for idx, urlStr := range req.URLs {
		slog.Info("downloading file from URL", "index", idx, "url", urlStr)

		// Get filename from URL
		filename := fmt.Sprintf("image_%d.jpg", idx)
		if parsedURL, err := url.Parse(urlStr); err == nil {
			if base := filepath.Base(parsedURL.Path); base != "." && base != "/" {
				filename = base
			}
		}

		// Perform HTTP GET request to download file
		resp, err := httpClient.Get(urlStr)
		if err != nil {
			slog.Error("failed to download file from URL", "url", urlStr, "error", err)
			rollbackUploadedInstances(uploadedInstanceIDs)
			model.WriteError(w, http.StatusBadGateway, "DOWNLOAD_FAILED",
				fmt.Sprintf("Failed to download file from URL: %s", urlStr), err.Error())
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			slog.Error("download URL returned non-200 status", "url", urlStr, "status", resp.StatusCode)
			rollbackUploadedInstances(uploadedInstanceIDs)
			model.WriteError(w, http.StatusBadGateway, "DOWNLOAD_BAD_STATUS",
				fmt.Sprintf("Download URL returned status %d: %s", resp.StatusCode, urlStr), "")
			return
		}

		// Save downloaded bytes to temp file
		inputFilePath := filepath.Join(tempDir, fmt.Sprintf("%d_%s", idx, service.SanitizeFilename(filename)))
		out, err := os.Create(inputFilePath)
		if err != nil {
			slog.Error("failed to create temp file", "error", err)
			rollbackUploadedInstances(uploadedInstanceIDs)
			model.WriteError(w, http.StatusInternalServerError, "FILE_SAVE_ERROR",
				"Failed to save downloaded file locally", "")
			return
		}

		if _, err := io.Copy(out, resp.Body); err != nil {
			out.Close()
			slog.Error("failed to write temp file", "error", err)
			rollbackUploadedInstances(uploadedInstanceIDs)
			model.WriteError(w, http.StatusInternalServerError, "FILE_WRITE_ERROR",
				"Failed to write downloaded file locally", "")
			return
		}
		out.Close()

		outputFilePath := filepath.Join(tempDir, fmt.Sprintf("output_%d.dcm", idx))

		// ── Step 1: Convert to DICOM ────────────────────────────────────────
		if err := convertToDICOM(fileType, inputFilePath, outputFilePath, tempDir, filename, paramsStr); err != nil {
			rollbackUploadedInstances(uploadedInstanceIDs)
			model.WriteError(w, http.StatusInternalServerError, "CONVERSION_FAILED",
				fmt.Sprintf("DICOM conversion failed for file %s", filename), err.Error())
			return
		}

		// ── Step 2: Upload to Orthanc ───────────────────────────────────────
		uploadResp, err := service.UploadInstance(&OrthancCfg, outputFilePath)
		if err != nil {
			rollbackUploadedInstances(uploadedInstanceIDs)
			model.WriteError(w, http.StatusBadGateway, "ORTHANC_UPLOAD_FAILED",
				"Failed to upload DICOM to Orthanc", err.Error())
			return
		}

		uploadedInstanceIDs = append(uploadedInstanceIDs, uploadResp.ID)
		lastUploadResp = uploadResp
		if parentStudyID == "" {
			parentStudyID = uploadResp.ParentStudy
		}
	}

	// ── Step 3: Modify study tags ───────────────────────────────────────
	if parentStudyID != "" {
		modifyResp, err := service.ModifyStudy(&OrthancCfg, parentStudyID, &req.OrthancModify)
		if err != nil {
			slog.Error("modify failed, rolling back uploads",
				"study_id", parentStudyID,
				"error", err,
			)
			rollbackUploadedInstances(uploadedInstanceIDs)
			model.WriteError(w, http.StatusBadGateway, "ORTHANC_MODIFY_FAILED",
				"Failed to modify study tags in Orthanc (uploads were rolled back)", err.Error())
			return
		}

		// ── Step 4: Return combined response ────────────────────────────────
		model.WriteJSON(w, http.StatusOK, SendToOrthancFromURLsResponse{
			Status: "success",
			Upload: lastUploadResp,
			Modify: modifyResp,
		})
		return
	}

	model.WriteError(w, http.StatusInternalServerError, "UNKNOWN_ERROR", "An unexpected error occurred during processing", "")
}

// rollbackUploadedInstances deletes all uploaded instances in case of any failure.
func rollbackUploadedInstances(ids []string) {
	for _, id := range ids {
		slog.Warn("rolling back instance upload", "instance_id", id)
		if err := service.DeleteInstance(&OrthancCfg, id); err != nil {
			slog.Error("rollback failed: could not delete instance", "instance_id", id, "error", err)
		}
	}
}
