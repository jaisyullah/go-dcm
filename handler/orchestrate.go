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
	"bytes"

	"dicom-converter-api/service"
)

// OrchestrateUploadAndSendRequest represents the full orchestration request.
// Covers both NEW uploads (via urls) and EXISTING study matching (via study_id).
type OrchestrateUploadAndSendRequest struct {
	// URLs to download and convert (for new uploads from webapps)
	URLs []string `json:"urls,omitempty"`

	// Existing Orthanc study ID (for already-uploaded studies)
	StudyID string `json:"study_id,omitempty"`

	// Filetype for conversion: img, pdf, cda, stl
	FileType string `json:"filetype"`

	// Conversion parameters (same as /convert/img2dcm parameters)
	Parameters json.RawMessage `json:"parameters,omitempty"`

	// Metadata tags to apply via Orthanc modify (KeepSource=true)
	OrthancModify service.OrthancModifyRequest `json:"orthanc_modify"`

	// Optional: AE Title to send study to after modification
	SendToModality string `json:"send_to_modality,omitempty"`

	// Optional: ACSN to set on the study after matching (for auto-correct)
	TargetAccessionNumber string `json:"target_accession_number,omitempty"`

	// Optional: URL to POST job completion result to (async callback)
	CallbackURL string `json:"callback_url,omitempty"`
}

// HandleOrchestrateUploadAndSend handles POST /api/v1/orchestrate/upload-and-send
// Composite endpoint: download -> convert -> upload -> modify -> send-to-modality.
func HandleOrchestrateUploadAndSend(w http.ResponseWriter, r *http.Request) {
	if !OrthancCfg.IsConfigured() {
		model.WriteError(w, http.StatusServiceUnavailable, "ORTHANC_NOT_CONFIGURED",
			"Orthanc is not configured. Set ORTHANC_URL environment variable.", "")
		return
	}

	var req OrchestrateUploadAndSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		model.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Failed to parse JSON body", err.Error())
		return
	}

	// Validate: must have either URLs or StudyID
	if len(req.URLs) == 0 && req.StudyID == "" {
		model.WriteError(w, http.StatusBadRequest, "MISSING_TARGET",
			"Either 'urls' (for new upload) or 'study_id' (for existing study) must be provided", "")
		return
	}

	// Validate orthanc_modify
	if req.OrthancModify.Replace == nil && req.OrthancModify.Remove == nil {
		model.WriteError(w, http.StatusBadRequest, "MISSING_MODIFY",
			"'orthanc_modify' with at least Replace or Remove is required", "")
		return
	}

	// Validate filetype for new uploads
	if len(req.URLs) > 0 {
		fileType := strings.TrimSpace(strings.ToLower(req.FileType))
		if fileType == "" || !supportedFileTypes[fileType] {
			model.WriteError(w, http.StatusBadRequest, "INVALID_FILETYPE",
				"Invalid 'filetype' parameter. Must be one of: img, pdf, cda, stl", "")
			return
		}
	}

	// KeepSource=false for metadata-only modify — no demographic in payload
	req.OrthancModify.KeepSource = false
	req.OrthancModify.Force = true

	// Create background job
	jobID := service.CreateJob()

	service.EnqueueTask(service.Task{
		JobID: jobID,
		ExecuteFunc: func(ctx context.Context) (any, error) {
			result, err := executeOrchestration(ctx, &req)
			if err == nil && req.CallbackURL != "" {
				go notifyCallback(req.CallbackURL, result, jobID)
			}
			return result, err
		},
	})

	model.WriteJSON(w, http.StatusAccepted, SendToOrthancResponse{
		Status: "success",
		JobID:  jobID,
	})
}

// OrchestrationResult is the final job result payload.
type OrchestrationResult struct {
	StudyID          string                      `json:"study_id"`
	AccessionNumber  string                      `json:"accession_number"`
	IsNewUpload      bool                        `json:"is_new_upload"`
	ModifyResult     json.RawMessage             `json:"modify_result,omitempty"`
	SendToModalityOK bool                        `json:"send_to_modality_ok,omitempty"`
	UploadDetails    []service.OrthancUploadResponse `json:"upload_details,omitempty"`
}

func executeOrchestration(ctx context.Context, req *OrchestrateUploadAndSendRequest) (*OrchestrationResult, error) {
	var studyID string
	var isNewUpload bool
	var uploadedInstances []service.OrthancUploadResponse
	var uploadedIDs []string

	// PHASE 1: Upload new files or use existing study
	if len(req.URLs) > 0 {
		slog.InfoContext(ctx, "Phase 1: Downloading and converting URLs", "count", len(req.URLs))

		var paramsStr string
		if len(req.Parameters) > 0 {
			paramsStr = string(req.Parameters)
		}

		tempDir, err := os.MkdirTemp("", "orchestrate_*")
		if err != nil {
			return nil, fmt.Errorf("failed to create temp directory: %w", err)
		}
		defer os.RemoveAll(tempDir)

		httpClient := &http.Client{Timeout: 60 * time.Second}

		for idx, urlStr := range req.URLs {
			filename := fmt.Sprintf("image_%d.jpg", idx)
			if parsedURL, err := url.Parse(urlStr); err == nil {
				if base := filepath.Base(parsedURL.Path); base != "." && base != "/" {
					filename = base
				}
			}

			slog.InfoContext(ctx, "downloading", "index", idx, "url", urlStr)

			httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
			if err != nil {
				rollbackUploadedInstances(uploadedIDs)
				return nil, fmt.Errorf("failed to create download request: %w", err)
			}

			resp, err := httpClient.Do(httpReq)
			if err != nil {
				rollbackUploadedInstances(uploadedIDs)
				return nil, fmt.Errorf("failed to download %s: %w", urlStr, err)
			}

			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				rollbackUploadedInstances(uploadedIDs)
				return nil, fmt.Errorf("download %s returned status %d", urlStr, resp.StatusCode)
			}

			inputPath := filepath.Join(tempDir, fmt.Sprintf("%d_%s", idx, service.SanitizeFilename(filename)))
			out, err := os.Create(inputPath)
			if err != nil {
				resp.Body.Close()
				rollbackUploadedInstances(uploadedIDs)
				return nil, fmt.Errorf("failed to create temp file: %w", err)
			}
			if _, err := io.Copy(out, resp.Body); err != nil {
				out.Close()
				resp.Body.Close()
				rollbackUploadedInstances(uploadedIDs)
				return nil, fmt.Errorf("failed to save download: %w", err)
			}
			out.Close()
			resp.Body.Close()

			outputPath := filepath.Join(tempDir, fmt.Sprintf("output_%d.dcm", idx))

			if err := convertToDICOM(ctx, req.FileType, inputPath, outputPath, tempDir, filename, paramsStr); err != nil {
				rollbackUploadedInstances(uploadedIDs)
				return nil, fmt.Errorf("DICOM conversion failed for %s: %w", filename, err)
			}

			uploadResp, err := service.UploadInstance(&OrthancCfg, outputPath)
			if err != nil {
				rollbackUploadedInstances(uploadedIDs)
				return nil, fmt.Errorf("orthanc upload failed for %s: %w", filename, err)
			}

			uploadedIDs = append(uploadedIDs, uploadResp.ID)
			uploadedInstances = append(uploadedInstances, *uploadResp)

			if studyID == "" {
				studyID = uploadResp.ParentStudy
			}
		}

		isNewUpload = true
		slog.InfoContext(ctx, "Phase 1 complete", "study_id", studyID, "instances", len(uploadedIDs))
	} else {
		studyID = req.StudyID
		slog.InfoContext(ctx, "Phase 1: Using existing study", "study_id", studyID)
	}

	if studyID == "" {
		return nil, fmt.Errorf("no study ID resolved")
	}

	// PHASE 2: Modify study metadata (KeepSource=false, metadata-only)
	slog.InfoContext(ctx, "Phase 2: Modifying study metadata", "study_id", studyID)

	// Demographic tags are stripped from Replace if present, as they're
	// embedded during conversion. Only metadata tags should remain.
	if req.OrthancModify.Replace != nil {
		delete(req.OrthancModify.Replace, "PatientName")
		delete(req.OrthancModify.Replace, "PatientBirthDate")
		delete(req.OrthancModify.Replace, "PatientSex")
		delete(req.OrthancModify.Replace, "PatientID")
	}
	// Merge TargetAccessionNumber into Replace so a single
	// ModifyStudy (KeepSource=false) handles metadata and ACSN together,
	// avoiding orphan studies from sequential KeepSource=false calls.
	if req.TargetAccessionNumber != "" {
		if req.OrthancModify.Replace == nil {
			req.OrthancModify.Replace = make(map[string]any)
		}
		req.OrthancModify.Replace["AccessionNumber"] = req.TargetAccessionNumber
	}
	modifyResp, err := service.ModifyStudy(&OrthancCfg, studyID, &req.OrthancModify)
	if err != nil {
		if isNewUpload {
			slog.ErrorContext(ctx, "modify failed, rolling back uploads", "study_id", studyID, "error", err)
			rollbackUploadedInstances(uploadedIDs)
		}
		return nil, fmt.Errorf("orthanc modify failed: %w", err)
	}

	// PHASE 3: (removed — ACSN now merged into Phase 2 above)

	// PHASE 4: Send to DICOM router
	sendOK := false
	if req.SendToModality != "" {
		slog.InfoContext(ctx, "Phase 4: Sending to modality", "study_id", studyID, "ae", req.SendToModality)
		err := service.SendStudyToModality(&OrthancCfg, studyID, req.SendToModality)
		if err != nil {
			slog.WarnContext(ctx, "Phase 4: Send to modality failed (non-fatal)", "error", err)
		} else {
			sendOK = true
		}
	}

	// PHASE 5: Re-resolve study ID if ACSN changed (KeepSource:false creates new study)
	finalStudyID := studyID
	if req.TargetAccessionNumber != "" {
		if newID, err := service.FindStudyByAccession(&OrthancCfg, req.TargetAccessionNumber); err == nil && newID != "" {
			finalStudyID = newID
		}
	}

	result := &OrchestrationResult{
		StudyID:          finalStudyID,
		AccessionNumber:  req.TargetAccessionNumber,
		IsNewUpload:      isNewUpload,
		ModifyResult:     modifyResp,
		SendToModalityOK: sendOK,
	}

	if len(uploadedInstances) > 0 {
		result.UploadDetails = uploadedInstances
	}

	slog.InfoContext(ctx, "Orchestration complete",
		"study_id", finalStudyID,
		"is_new", isNewUpload,
		"send_ok", sendOK,
	)

	return result, nil
}

func notifyCallback(url string, result *OrchestrationResult, jobID string) {
	payload := map[string]any{
		"job_id": jobID,
		"status": "COMPLETED",
		"result": result,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("callback marshal failed", "url", url, "error", err)
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		slog.Warn("callback request failed", "url", url, "error", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		slog.Info("callback succeeded", "url", url, "status", resp.StatusCode)
	} else {
		slog.Warn("callback returned error", "url", url, "status", resp.StatusCode)
	}
}
