package handler

import (
	"context"
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

	"dicom-converter-api/model"
	"dicom-converter-api/service"
)

// SendToOrthancFromURLsRequest represents the JSON request payload for URL-based conversions.
type SendToOrthancFromURLsRequest struct {
	FileType      string                     `json:"filetype"` // "img", "pdf", "cda", "stl"
	URLs          []string                   `json:"urls"`
	Parameters    json.RawMessage            `json:"parameters,omitempty"`
	OrthancModify service.OrthancModifyRequest `json:"orthanc_modify"`
}

// HandleSendToOrthancFromURLs handles POST /api/v1/send-to-orthanc-from-urls
// Now refactored to be ASYNCHRONOUS.
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
	if fileType == "" || !supportedFileTypes[fileType] {
		model.WriteError(w, http.StatusBadRequest, "INVALID_FILETYPE",
			"Invalid 'filetype' parameter. Must be one of: img, pdf, cda, stl", "")
		return
	}

	// Validate URLs array
	if len(req.URLs) == 0 {
		model.WriteError(w, http.StatusBadRequest, "MISSING_URLS", "The 'urls' array must contain at least one URL link.", "")
		return
	}

	// 1. Create Background Job
	jobID := service.CreateJob()

	// 2. Enqueue Task
	service.EnqueueTask(service.Task{
		JobID: jobID,
		ExecuteFunc: func(ctx context.Context) (any, error) {
			// Extract demographics for pre-emptive alignment
			var patientID, patientName, patientBirthDate, patientSex string
			if req.OrthancModify.Replace != nil {
				if pid, ok := req.OrthancModify.Replace["PatientID"].(string); ok {
					patientID = pid
				}
				if name, ok := req.OrthancModify.Replace["PatientName"].(string); ok {
					patientName = name
				}
				if dob, ok := req.OrthancModify.Replace["PatientBirthDate"].(string); ok {
					patientBirthDate = dob
				}
				if sex, ok := req.OrthancModify.Replace["PatientSex"].(string); ok {
					patientSex = sex
				}
			}

			// Pre-emptively align patient demographics in Orthanc if they mismatch
			if patientID != "" {
				if err := service.PreemptiveAlignPatientDemographics(&OrthancCfg, patientID, patientName, patientBirthDate, patientSex); err != nil {
					return nil, fmt.Errorf("demographic alignment failed: %w", err)
				}
			}

			// Convert parameters raw message to string
			var paramsStr string
			if len(req.Parameters) > 0 {
				paramsStr = string(req.Parameters)
			}

			// Create temp directory for this batch
			tempDir, err := os.MkdirTemp("", "send_to_orthanc_urls_*")
			if err != nil {
				return nil, fmt.Errorf("failed to create temp directory: %w", err)
			}
			defer os.RemoveAll(tempDir)

			var uploadedInstanceIDs []string
			var parentStudyID string
			var lastUploadResp *service.OrthancUploadResponse

			httpClient := &http.Client{Timeout: 60 * time.Second}

			// Download, convert, and upload each URL
			for idx, urlStr := range req.URLs {
				slog.InfoContext(ctx, "downloading file from URL", "index", idx, "url", urlStr)

				filename := fmt.Sprintf("image_%d.jpg", idx)
				if parsedURL, err := url.Parse(urlStr); err == nil {
					if base := filepath.Base(parsedURL.Path); base != "." && base != "/" {
						filename = base
					}
				}

				// Create a cancellable request using the job context
				httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
				if err != nil {
					rollbackUploadedInstances(uploadedInstanceIDs)
					return nil, fmt.Errorf("failed to create download request for %s: %w", urlStr, err)
				}

				resp, err := httpClient.Do(httpReq)
				if err != nil {
					rollbackUploadedInstances(uploadedInstanceIDs)
					return nil, fmt.Errorf("failed to download %s: %w", urlStr, err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					rollbackUploadedInstances(uploadedInstanceIDs)
					return nil, fmt.Errorf("download URL %s returned status %d", urlStr, resp.StatusCode)
				}

				inputFilePath := filepath.Join(tempDir, fmt.Sprintf("%d_%s", idx, service.SanitizeFilename(filename)))
				out, err := os.Create(inputFilePath)
				if err != nil {
					rollbackUploadedInstances(uploadedInstanceIDs)
					return nil, fmt.Errorf("failed to create temp file for %s: %w", filename, err)
				}

				if _, err := io.Copy(out, resp.Body); err != nil {
					out.Close()
					rollbackUploadedInstances(uploadedInstanceIDs)
					return nil, fmt.Errorf("failed to save download %s: %w", filename, err)
				}
				out.Close()

				outputFilePath := filepath.Join(tempDir, fmt.Sprintf("output_%d.dcm", idx))

				// Step 1: Convert to DICOM
				if err := convertToDICOM(ctx, fileType, inputFilePath, outputFilePath, tempDir, filename, paramsStr); err != nil {
					rollbackUploadedInstances(uploadedInstanceIDs)
					return nil, fmt.Errorf("DICOM conversion failed for %s: %w", filename, err)
				}

				// Step 2: Upload to Orthanc
				uploadResp, err := service.UploadInstance(&OrthancCfg, outputFilePath)
				if err != nil {
					rollbackUploadedInstances(uploadedInstanceIDs)
					return nil, fmt.Errorf("orthanc upload failed for %s: %w", filename, err)
				}

				uploadedInstanceIDs = append(uploadedInstanceIDs, uploadResp.ID)
				lastUploadResp = uploadResp
				if parentStudyID == "" {
					parentStudyID = uploadResp.ParentStudy
				}
			}

			// Step 3: Modify study tags
			if parentStudyID != "" {
				// Strip patient demographics from Replace payload because they were already embedded
				// in the DICOM instances during conversion. This avoids triggering demographic mismatch (400)
				// and patient-level modify (500) errors in Orthanc under SQLite locks.
				if req.OrthancModify.Replace != nil {
					delete(req.OrthancModify.Replace, "PatientName")
					delete(req.OrthancModify.Replace, "PatientBirthDate")
					delete(req.OrthancModify.Replace, "PatientSex")
					delete(req.OrthancModify.Replace, "PatientID")
				}

				modifyResp, err := service.ModifyStudy(&OrthancCfg, parentStudyID, &req.OrthancModify)
				if err != nil {
					slog.ErrorContext(ctx, "modify failed, rolling back uploads", "study_id", parentStudyID, "error", err)
					rollbackUploadedInstances(uploadedInstanceIDs)
					return nil, fmt.Errorf("orthanc modify failed: %w", err)
				}

				return SendToOrthancResult{
					Upload: lastUploadResp,
					Modify: modifyResp,
				}, nil
			}

			return nil, fmt.Errorf("no parent study ID resolved")
		},
	})

	// 3. Respond with 202 Accepted and JobID
	model.WriteJSON(w, http.StatusAccepted, SendToOrthancResponse{
		Status: "success",
		JobID:  jobID,
	})
}

// rollbackUploadedInstances deletes all uploaded instances in case of any failure.
func rollbackUploadedInstances(ids []string) {
	for _, id := range ids {
		slog.Warn("rolling back instance upload", "instance_id", id)
		_ = service.DeleteInstance(&OrthancCfg, id)
	}
}
