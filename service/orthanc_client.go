package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// OrthancConfig holds the connection settings for an Orthanc server.
type OrthancConfig struct {
	URL  string // e.g. "http://orthanc"
	Port string // e.g. "8042"
	User string // Basic auth username (optional)
	Pass string // Basic auth password (optional)
}

// BaseURL returns the full base URL for the Orthanc REST API.
func (c *OrthancConfig) BaseURL() string {
	return fmt.Sprintf("%s:%s", c.URL, c.Port)
}

// IsConfigured returns true if the Orthanc URL is set.
func (c *OrthancConfig) IsConfigured() bool {
	return c.URL != ""
}

// LoadOrthancConfig reads Orthanc connection settings from environment variables.
func LoadOrthancConfig() OrthancConfig {
	return OrthancConfig{
		URL:  getEnvDefault("ORTHANC_URL", ""),
		Port: getEnvDefault("ORTHANC_PORT", "8042"),
		User: getEnvDefault("ORTHANC_USER", ""),
		Pass: getEnvDefault("ORTHANC_PASS", ""),
	}
}

func getEnvDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// OrthancUploadResponse represents the JSON response from POST /instances.
type OrthancUploadResponse struct {
	ID            string `json:"ID"`
	ParentPatient string `json:"ParentPatient"`
	ParentSeries  string `json:"ParentSeries"`
	ParentStudy   string `json:"ParentStudy"`
	Path          string `json:"Path"`
	Status        string `json:"Status"`
}

// OrthancModifyRequest represents the payload for POST /studies/{id}/modify.
// Replace values are any for Orthanc JSON compatibility: strings for simple tags,
// arrays of objects for sequences (e.g. ScheduledProcedureStepSequence).
type OrthancModifyRequest struct {
	Replace     map[string]any    `json:"Replace,omitempty"`
	Remove      []string          `json:"Remove,omitempty"`
	Keep        []string          `json:"Keep,omitempty"`
	KeepSource  bool              `json:"KeepSource"`
	KeepLabels  bool              `json:"KeepLabels"`
	Force       bool              `json:"Force"`
	Synchronous bool              `json:"Synchronous"`
}

// orthancHTTPClient is a shared HTTP client with sensible timeouts for Orthanc calls.
var orthancHTTPClient = &http.Client{
	Timeout: 120 * time.Second,
}

// newOrthancRequest creates an HTTP request with optional Basic Auth.
func newOrthancRequest(method, url string, body io.Reader, config *OrthancConfig) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	if config.User != "" && config.Pass != "" {
		req.SetBasicAuth(config.User, config.Pass)
	}

	return req, nil
}

// UploadInstance uploads a DICOM file to Orthanc via POST /instances with automatic retry.
func UploadInstance(config *OrthancConfig, dcmFilePath string) (*OrthancUploadResponse, error) {
	if !config.IsConfigured() {
		return nil, fmt.Errorf("orthanc is not configured (ORTHANC_URL is empty)")
	}

	// Read the DCM file
	dcmData, err := os.ReadFile(dcmFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read DICOM file: %w", err)
	}

	url := fmt.Sprintf("%s/instances", config.BaseURL())
	var uploadResp OrthancUploadResponse
	var lastErr error
	maxRetries := 5
	backoff := 1 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := newOrthancRequest(http.MethodPost, url, bytes.NewReader(dcmData), config)
		if err != nil {
			return nil, fmt.Errorf("failed to create upload request: %w", err)
		}
		req.Header.Set("Content-Type", "application/dicom")

		slog.Info("uploading DICOM instance to Orthanc", "url", url, "file_size", len(dcmData), "attempt", attempt)

		resp, err := orthancHTTPClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("failed to upload to Orthanc: %w", err)
			slog.Warn("Orthanc upload failed with network error, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read Orthanc upload response: %w", err)
			slog.Warn("Failed to read Orthanc response body, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("Orthanc upload failed with status %d: %s", resp.StatusCode, string(respBody))
			if resp.StatusCode >= 500 {
				slog.Warn("Orthanc upload returned server error, retrying...", "status", resp.StatusCode, "attempt", attempt)
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			// For 4xx errors, fail immediately
			return nil, lastErr
		}

		if err := json.Unmarshal(respBody, &uploadResp); err != nil {
			return nil, fmt.Errorf("failed to parse Orthanc upload response: %w", err)
		}

		slog.Info("DICOM instance uploaded to Orthanc",
			"instance_id", uploadResp.ID,
			"parent_study", uploadResp.ParentStudy,
			"status", uploadResp.Status,
		)
		return &uploadResp, nil
	}

	return nil, fmt.Errorf("failed after %d upload attempts: %w", maxRetries, lastErr)
}

// ModifyStudy modifies DICOM tags on a study via POST /studies/{id}/modify.
// Implements automatic retries on transient errors and auto-aligns patient demographic mismatches.
func ModifyStudy(config *OrthancConfig, studyID string, modifyReq *OrthancModifyRequest) (json.RawMessage, error) {
	if !config.IsConfigured() {
		return nil, fmt.Errorf("orthanc is not configured (ORTHANC_URL is empty)")
	}

	// Force synchronous for reliable response
	modifyReq.Synchronous = true

	var lastErr error
	maxRetries := 5
	backoff := 1 * time.Second
	aligned := false

	for attempt := 1; attempt <= maxRetries; attempt++ {
		payload, err := json.Marshal(modifyReq)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal modify request: %w", err)
		}

		url := fmt.Sprintf("%s/studies/%s/modify", config.BaseURL(), studyID)

		req, err := newOrthancRequest(http.MethodPost, url, bytes.NewReader(payload), config)
		if err != nil {
			return nil, fmt.Errorf("failed to create modify request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		slog.Info("modifying study tags in Orthanc",
			"url", url,
			"study_id", studyID,
			"attempt", attempt,
			"replace_tag_count", len(modifyReq.Replace),
		)

		resp, err := orthancHTTPClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("failed to send modify request to Orthanc: %w", err)
			slog.Warn("Orthanc modify failed with network error, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read Orthanc modify response: %w", err)
			slog.Warn("Failed to read Orthanc modify response body, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("Orthanc modify failed with status %d: %s", resp.StatusCode, string(respBody))
			
			// 1. Self-recovery: check for demographic mismatch (HTTP 400 Bad Request)
			if resp.StatusCode == http.StatusBadRequest && !aligned &&
				(strings.Contains(string(respBody), "Trying to change patient tags in a study") ||
					strings.Contains(string(respBody), "All the 'Replace' tags should match")) {
				
				slog.Warn("Demographic mismatch detected. Attempting self-recovery by updating patient demographics in Orthanc (Option B)...")
				
				patientID, okPID := modifyReq.Replace["PatientID"].(string)
				patientName, _ := modifyReq.Replace["PatientName"].(string)
				patientBirthDate, _ := modifyReq.Replace["PatientBirthDate"].(string)
				patientSex, _ := modifyReq.Replace["PatientSex"].(string)
				studyDate, _ := modifyReq.Replace["StudyDate"].(string)
				modality, _ := modifyReq.Replace["Modality"].(string)
				
				if okPID && patientID != "" {
					patientInternalID, err := findPatientInternalID(config, patientID)
					if err == nil {
						slog.Info("Self-recovery: Found patient internal ID in Orthanc. Overwriting demographics...",
							"patient_id", patientID,
							"internal_id", patientInternalID,
							"name", patientName,
							"dob", patientBirthDate,
							"sex", patientSex,
						)
						
						err = modifyPatient(config, patientInternalID, patientName, patientBirthDate, patientSex)
						if err == nil {
							slog.Info("Self-recovery: Patient demographics successfully updated on Orthanc.")
							
							// Wait a short moment for indexing
							time.Sleep(1 * time.Second)
							
							// Re-resolve the Study ID using PatientID, StudyDate, and Modality
							newStudyID, err := findStudyIDByCriteria(config, patientID, studyDate, modality)
							if err == nil {
								slog.Info("Self-recovery: Successfully re-resolved new Study ID after patient update", "new_study_id", newStudyID)
								studyID = newStudyID
								aligned = true
								
								// Reset retry counter to give the corrected request clean attempts
								attempt = 0
								continue
							} else {
								slog.Error("Self-recovery failed: could not re-resolve Study ID after patient update", "error", err)
							}
						} else {
							slog.Error("Self-recovery failed: could not modify patient on Orthanc", "error", err)
						}
					} else {
						slog.Error("Self-recovery failed: patient not found in Orthanc", "patient_id", patientID, "error", err)
					}
				}
			}

			// For all server (5xx) and client (4xx) errors, fail immediately to prevent study duplication under SQLite locks
			cleanupDuplicateStudyOnFailure(config, studyID, modifyReq)
			return nil, lastErr
		}

		slog.Info("study tags modified successfully in Orthanc", "study_id", studyID)
		return json.RawMessage(respBody), nil
	}

	cleanupDuplicateStudyOnFailure(config, studyID, modifyReq)
	return nil, fmt.Errorf("failed after %d modify attempts: %w", maxRetries, lastErr)
}

// DeleteStudy removes a study from Orthanc via DELETE /studies/{id}.
// Used for rollback when modification fails or cleanup of duplicate/failed studies.
func DeleteStudy(config *OrthancConfig, studyID string) error {
	if !config.IsConfigured() {
		return fmt.Errorf("orthanc is not configured")
	}

	url := fmt.Sprintf("%s/studies/%s", config.BaseURL(), studyID)

	req, err := newOrthancRequest(http.MethodDelete, url, nil, config)
	if err != nil {
		return fmt.Errorf("failed to create delete request: %w", err)
	}

	slog.Info("deleting study from Orthanc", "study_id", studyID)

	resp, err := orthancHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete study from Orthanc: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Orthanc delete study failed with status %d: %s", resp.StatusCode, string(body))
	}

	slog.Info("study deleted from Orthanc successfully", "study_id", studyID)
	return nil
}

// FindStudyIDByUID queries Orthanc's POST /tools/find to retrieve a Study's internal ID by its StudyInstanceUID.
func FindStudyIDByUID(config *OrthancConfig, studyUID string) (string, error) {
	if !config.IsConfigured() {
		return "", fmt.Errorf("orthanc is not configured")
	}

	url := fmt.Sprintf("%s/tools/find", config.BaseURL())
	queryPayload := map[string]any{
		"Level":  "Study",
		"Expand": false,
		"Query": map[string]string{
			"StudyInstanceUID": studyUID,
		},
	}
	bodyBytes, err := json.Marshal(queryPayload)
	if err != nil {
		return "", err
	}

	maxRetries := 5
	backoff := 1 * time.Second
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := newOrthancRequest(http.MethodPost, url, bytes.NewReader(bodyBytes), config)
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := orthancHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			slog.Warn("FindStudyIDByUID network error, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			slog.Warn("FindStudyIDByUID failed to read body, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("find study by UID failed with status %d: %s", resp.StatusCode, string(respBody))
			if resp.StatusCode >= 500 {
				slog.Warn("FindStudyIDByUID server error, retrying...", "status", resp.StatusCode, "attempt", attempt)
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return "", lastErr
		}

		var studyUUIDs []string
		if err := json.Unmarshal(respBody, &studyUUIDs); err != nil {
			return "", err
		}
		if len(studyUUIDs) == 0 {
			return "", fmt.Errorf("study not found by UID: %s", studyUID)
		}
		return studyUUIDs[0], nil
	}

	return "", fmt.Errorf("failed to find study by UID after %d attempts: %w", maxRetries, lastErr)
}

// cleanupDuplicateStudyOnFailure checks if a target study was partially/fully created during a failed modify job and deletes it.
// It ensures that the original study is safe and not deleted.
func cleanupDuplicateStudyOnFailure(config *OrthancConfig, originalStudyID string, modifyReq *OrthancModifyRequest) {
	if modifyReq == nil || modifyReq.Replace == nil {
		return
	}

	targetStudyUID, ok := modifyReq.Replace["StudyInstanceUID"].(string)
	if !ok || targetStudyUID == "" {
		return
	}

	slog.Warn("ModifyStudy failed; checking for orphaned/duplicate study in Orthanc to clean up...", "target_uid", targetStudyUID)

	// Look up if a study exists with the target StudyInstanceUID
	targetStudyID, err := FindStudyIDByUID(config, targetStudyUID)
	if err != nil {
		slog.Info("No orphaned/duplicate study found to clean up", "target_uid", targetStudyUID, "error", err.Error())
		return
	}

	// Double-check to ensure we NEVER delete the original study!
	if targetStudyID == originalStudyID {
		slog.Info("Target study ID matches original study ID. Skipping cleanup to keep the original study safe.", "study_id", originalStudyID)
		return
	}

	slog.Warn("Found orphaned/duplicate study from failed modify, deleting it to prevent pollution...", "target_study_id", targetStudyID, "original_study_id", originalStudyID)
	if err := DeleteStudy(config, targetStudyID); err != nil {
		slog.Error("Failed to delete orphaned/duplicate study during rollback", "target_study_id", targetStudyID, "error", err)
	} else {
		slog.Info("Successfully cleaned up orphaned/duplicate study in Orthanc", "target_study_id", targetStudyID)
	}
}

// DeleteInstance removes an instance from Orthanc via DELETE /instances/{id}.
// Used for rollback when modify fails after a successful upload.
func DeleteInstance(config *OrthancConfig, instanceID string) error {
	if !config.IsConfigured() {
		return fmt.Errorf("orthanc is not configured")
	}

	url := fmt.Sprintf("%s/instances/%s", config.BaseURL(), instanceID)

	req, err := newOrthancRequest(http.MethodDelete, url, nil, config)
	if err != nil {
		return fmt.Errorf("failed to create delete request: %w", err)
	}

	slog.Info("rolling back: deleting instance from Orthanc", "instance_id", instanceID)

	resp, err := orthancHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete instance from Orthanc: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Orthanc delete failed with status %d: %s", resp.StatusCode, string(body))
	}

	slog.Info("instance deleted from Orthanc (rollback complete)", "instance_id", instanceID)
	return nil
}

// PingOrthanc checks Orthanc connectivity via GET /system.
func PingOrthanc(config *OrthancConfig) error {
	if !config.IsConfigured() {
		return fmt.Errorf("orthanc is not configured (ORTHANC_URL is empty)")
	}

	url := fmt.Sprintf("%s/system", config.BaseURL())

	req, err := newOrthancRequest(http.MethodGet, url, nil, config)
	if err != nil {
		return fmt.Errorf("failed to create ping request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("orthanc unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("orthanc returned status %d", resp.StatusCode)
	}

	return nil
}

// PatientFindResult defines the fields we care about in Orthanc's POST /tools/find response.
type PatientFindResult struct {
	ID            string `json:"ID"`
	MainDicomTags struct {
		PatientBirthDate string `json:"PatientBirthDate"`
		PatientID        string `json:"PatientID"`
		PatientName      string `json:"PatientName"`
		PatientSex       string `json:"PatientSex"`
	} `json:"MainDicomTags"`
}

// findExistingPatientDemographics queries Orthanc's POST /tools/find to retrieve existing patient demographics.
func findExistingPatientDemographics(config *OrthancConfig, patientID string) (map[string]string, error) {
	url := fmt.Sprintf("%s/tools/find", config.BaseURL())
	queryPayload := map[string]any{
		"Level":  "Patient",
		"Expand": true,
		"Query": map[string]string{
			"PatientID": patientID,
		},
	}
	bodyBytes, err := json.Marshal(queryPayload)
	if err != nil {
		return nil, err
	}

	req, err := newOrthancRequest(http.MethodPost, url, bytes.NewReader(bodyBytes), config)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := orthancHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("find patient failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var results []PatientFindResult
	if err := json.Unmarshal(respBody, &results); err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("patient not found in Orthanc")
	}

	demographics := map[string]string{
		"PatientName":      results[0].MainDicomTags.PatientName,
		"PatientBirthDate": results[0].MainDicomTags.PatientBirthDate,
		"PatientSex":       results[0].MainDicomTags.PatientSex,
	}
	return demographics, nil
}

// findPatientInternalID queries Orthanc's POST /tools/find to retrieve the patient's internal Orthanc resource ID.
func findPatientInternalID(config *OrthancConfig, patientID string) (string, error) {
	url := fmt.Sprintf("%s/tools/find", config.BaseURL())
	queryPayload := map[string]any{
		"Level":  "Patient",
		"Expand": true,
		"Query": map[string]string{
			"PatientID": patientID,
		},
	}
	bodyBytes, err := json.Marshal(queryPayload)
	if err != nil {
		return "", err
	}

	maxRetries := 5
	backoff := 1 * time.Second
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := newOrthancRequest(http.MethodPost, url, bytes.NewReader(bodyBytes), config)
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := orthancHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			slog.Warn("findPatientInternalID network error, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			slog.Warn("findPatientInternalID failed to read body, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("find patient internal ID failed with status %d: %s", resp.StatusCode, string(respBody))
			if resp.StatusCode >= 500 {
				slog.Warn("findPatientInternalID server error, retrying...", "status", resp.StatusCode, "attempt", attempt)
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return "", lastErr
		}

		var results []PatientFindResult
		if err := json.Unmarshal(respBody, &results); err != nil {
			return "", err
		}
		if len(results) == 0 {
			return "", fmt.Errorf("patient not found in Orthanc")
		}
		return results[0].ID, nil
	}

	return "", fmt.Errorf("failed to find patient internal ID after %d attempts: %w", maxRetries, lastErr)
}

// JobResponse represents the response from an asynchronous Orthanc job creation.
type JobResponse struct {
	ID   string `json:"ID"`
	Path string `json:"Path"`
}

// OrthancJobStatus represents the response from GET /jobs/{id}.
type OrthancJobStatus struct {
	State string `json:"State"`
}

func waitForJobCompletion(config *OrthancConfig, jobID string) error {
	url := fmt.Sprintf("%s/jobs/%s", config.BaseURL(), jobID)
	timeout := time.After(60 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for Orthanc job %s", jobID)
		case <-ticker.C:
			req, err := newOrthancRequest(http.MethodGet, url, nil, config)
			if err != nil {
				return err
			}
			resp, err := orthancHTTPClient.Do(req)
			if err != nil {
				continue
			}

			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				continue
			}

			bodyBytes, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				continue
			}

			var status OrthancJobStatus
			if err := json.Unmarshal(bodyBytes, &status); err != nil {
				continue
			}

			switch status.State {
			case "Success":
				return nil
			case "Failure":
				return fmt.Errorf("Orthanc job %s failed in background", jobID)
			}
		}
	}
}

// modifyPatient updates patient-level demographics in Orthanc asynchronously.
func modifyPatient(config *OrthancConfig, patientInternalID string, name, birthDate, sex string) error {
	url := fmt.Sprintf("%s/patients/%s/modify", config.BaseURL(), patientInternalID)
	replace := map[string]string{}
	if name != "" {
		replace["PatientName"] = name
	}
	if birthDate != "" {
		replace["PatientBirthDate"] = birthDate
	}
	if sex != "" {
		replace["PatientSex"] = sex
	}
	payload := map[string]any{
		"Replace":      replace,
		"KeepSource":   false,
		"Force":        true,
		"Asynchronous": true,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	maxRetries := 5
	backoff := 1 * time.Second
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := newOrthancRequest(http.MethodPost, url, bytes.NewReader(bodyBytes), config)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := orthancHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			slog.Warn("modifyPatient network error, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			slog.Warn("modifyPatient failed to read body, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("patient modify failed with status %d: %s", resp.StatusCode, string(respBody))
			return lastErr
		}

		var jobResp JobResponse
		if err := json.Unmarshal(respBody, &jobResp); err != nil {
			return fmt.Errorf("failed to parse job creation response: %w", err)
		}

		slog.Info("Orthanc background modify job created successfully", "job_id", jobResp.ID)
		return waitForJobCompletion(config, jobResp.ID)
	}

	return fmt.Errorf("failed to modify patient after %d attempts: %w", maxRetries, lastErr)
}

// findStudyIDByCriteria queries Orthanc's POST /tools/find to retrieve a Study ID matching PatientID, StudyDate, and Modality.
func findStudyIDByCriteria(config *OrthancConfig, patientID, studyDate, modality string) (string, error) {
	url := fmt.Sprintf("%s/tools/find", config.BaseURL())
	query := map[string]string{
		"PatientID": patientID,
	}
	if studyDate != "" {
		query["StudyDate"] = studyDate
	}
	if modality != "" {
		query["ModalitiesInStudy"] = modality
	}
	queryPayload := map[string]any{
		"Level":  "Study",
		"Expand": true,
		"Query":  query,
	}
	bodyBytes, err := json.Marshal(queryPayload)
	if err != nil {
		return "", err
	}

	maxRetries := 5
	backoff := 1 * time.Second
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := newOrthancRequest(http.MethodPost, url, bytes.NewReader(bodyBytes), config)
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := orthancHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			slog.Warn("findStudyIDByCriteria network error, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			slog.Warn("findStudyIDByCriteria failed to read body, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("find study by criteria failed with status %d: %s", resp.StatusCode, string(respBody))
			if resp.StatusCode >= 500 {
				slog.Warn("findStudyIDByCriteria server error, retrying...", "status", resp.StatusCode, "attempt", attempt)
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return "", lastErr
		}

		var results []struct {
			ID string `json:"ID"`
		}
		if err := json.Unmarshal(respBody, &results); err != nil {
			return "", err
		}
		if len(results) == 0 {
			lastErr = fmt.Errorf("study not found by criteria")
			slog.Warn("findStudyIDByCriteria: study not indexed yet, retrying...", "attempt", attempt)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}
		return results[0].ID, nil
	}

	return "", fmt.Errorf("failed to find study after %d attempts: %w", maxRetries, lastErr)
}

var (
	patientLocks   = make(map[string]*sync.Mutex)
	patientLocksMu sync.Mutex
)

func getPatientLock(patientID string) *sync.Mutex {
	patientLocksMu.Lock()
	defer patientLocksMu.Unlock()

	lock, ok := patientLocks[patientID]
	if !ok {
		lock = &sync.Mutex{}
		patientLocks[patientID] = lock
	}
	return lock
}

// findPatientDetails queries Orthanc's POST /tools/find to retrieve the patient's internal ID and main tags.
func findPatientDetails(config *OrthancConfig, patientID string) (*PatientFindResult, error) {
	url := fmt.Sprintf("%s/tools/find", config.BaseURL())
	queryPayload := map[string]any{
		"Level":  "Patient",
		"Expand": true,
		"Query": map[string]string{
			"PatientID": patientID,
		},
	}
	bodyBytes, err := json.Marshal(queryPayload)
	if err != nil {
		return nil, err
	}

	maxRetries := 5
	backoff := 1 * time.Second
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := newOrthancRequest(http.MethodPost, url, bytes.NewReader(bodyBytes), config)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := orthancHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			slog.Warn("findPatientDetails network error, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			slog.Warn("findPatientDetails failed to read body, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("find patient details failed with status %d: %s", resp.StatusCode, string(respBody))
			if resp.StatusCode >= 500 {
				slog.Warn("findPatientDetails server error, retrying...", "status", resp.StatusCode, "attempt", attempt)
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return nil, lastErr
		}

		var results []PatientFindResult
		if err := json.Unmarshal(respBody, &results); err != nil {
			return nil, err
		}
		if len(results) == 0 {
			lastErr = fmt.Errorf("patient not found in Orthanc (indexing delay)")
			slog.Warn("findPatientDetails: patient not indexed yet, retrying...", "patient_id", patientID, "attempt", attempt)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}
		return &results[0], nil
	}

	return nil, fmt.Errorf("failed to find patient details after %d attempts: %w", maxRetries, lastErr)
}

// findPatientStudies queries Orthanc's POST /tools/find to retrieve all Study UUIDs for a given PatientID.
func findPatientStudies(config *OrthancConfig, patientID string) ([]string, error) {
	url := fmt.Sprintf("%s/tools/find", config.BaseURL())
	queryPayload := map[string]any{
		"Level":  "Study",
		"Expand": false,
		"Query": map[string]string{
			"PatientID": patientID,
		},
	}
	bodyBytes, err := json.Marshal(queryPayload)
	if err != nil {
		return nil, err
	}

	maxRetries := 5
	backoff := 1 * time.Second
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := newOrthancRequest(http.MethodPost, url, bytes.NewReader(bodyBytes), config)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := orthancHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			slog.Warn("findPatientStudies network error, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			slog.Warn("findPatientStudies failed to read body, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("find patient studies failed with status %d: %s", resp.StatusCode, string(respBody))
			if resp.StatusCode >= 500 {
				slog.Warn("findPatientStudies server error, retrying...", "status", resp.StatusCode, "attempt", attempt)
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return nil, lastErr
		}

		var studyUUIDs []string
		if err := json.Unmarshal(respBody, &studyUUIDs); err != nil {
			return nil, err
		}
		return studyUUIDs, nil
	}

	return nil, fmt.Errorf("failed to find patient studies after %d attempts: %w", maxRetries, lastErr)
}

// PreemptiveAlignPatientDemographics aligns existing patient demographics in Orthanc with EMR values before upload.
// This prevents demographic mismatch and SQLite lock conflicts during subsequent uploads/modifications.
func PreemptiveAlignPatientDemographics(config *OrthancConfig, patientID, name, birthDate, sex string) error {
	// Obtain patient lock to serialize demographic changes for the same patient ID
	lock := getPatientLock(patientID)
	lock.Lock()
	defer lock.Unlock()

	slog.Info("Pre-emptive demographic alignment check started", "patient_id", patientID, "name", name, "birth_date", birthDate, "sex", sex)

	// 1. Query current details in Orthanc
	url := fmt.Sprintf("%s/tools/find", config.BaseURL())
	queryPayload := map[string]any{
		"Level":  "Patient",
		"Expand": true,
		"Query": map[string]string{
			"PatientID": patientID,
		},
	}
	bodyBytes, err := json.Marshal(queryPayload)
	if err != nil {
		return err
	}

	req, err := newOrthancRequest(http.MethodPost, url, bytes.NewReader(bodyBytes), config)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := orthancHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to query patient in Orthanc: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("patient query failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var results []PatientFindResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return fmt.Errorf("failed to parse patient query response: %w", err)
	}

	// If the patient does not exist, there's nothing to align. The upcoming upload will create it cleanly.
	if len(results) == 0 {
		slog.Info("Pre-emptive demographic alignment skipped: patient does not exist in Orthanc yet", "patient_id", patientID)
		return nil
	}

	patientDetails := results[0]

	// 2. Check if demographics match
	if patientDetails.MainDicomTags.PatientName == name &&
		patientDetails.MainDicomTags.PatientBirthDate == birthDate &&
		patientDetails.MainDicomTags.PatientSex == sex {
		slog.Info("Pre-emptive demographic alignment skipped: demographics already match target", "patient_id", patientID, "name", name)
		return nil
	}

	slog.Warn("Pre-emptive demographic mismatch detected! Aligning existing studies...",
		"patient_id", patientID,
		"current_name", patientDetails.MainDicomTags.PatientName, "target_name", name,
		"current_birthdate", patientDetails.MainDicomTags.PatientBirthDate, "target_birthdate", birthDate,
		"current_sex", patientDetails.MainDicomTags.PatientSex, "target_sex", sex,
	)

	// 3. Get all study UUIDs for this patient ID
	studyUUIDs, err := findPatientStudies(config, patientID)
	if err != nil {
		return fmt.Errorf("failed to fetch studies for alignment: %w", err)
	}

	if len(studyUUIDs) == 0 {
		slog.Info("Pre-emptive demographic alignment skipped: patient exists but has no studies", "patient_id", patientID)
		return nil
	}

	// 4. Modify each study asynchronously and wait for completion
	replaceMap := map[string]string{
		"PatientName":      name,
		"PatientBirthDate": birthDate,
	}
	if sex != "" {
		replaceMap["PatientSex"] = sex
	}

	payload := map[string]any{
		"Replace":     replaceMap,
		"Remove":      []string{},
		"Keep":        []string{},
		"KeepSource":  false,
		"KeepLabels":  true,
		"Force":       true,
		"Synchronous": false,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	for _, studyUUID := range studyUUIDs {
		modifyURL := fmt.Sprintf("%s/studies/%s/modify", config.BaseURL(), studyUUID)

		maxRetries := 5
		backoff := 1 * time.Second
		var lastModifyErr error
		var jobResp JobResponse

		for attempt := 1; attempt <= maxRetries; attempt++ {
			modifyReq, err := newOrthancRequest(http.MethodPost, modifyURL, bytes.NewReader(payloadBytes), config)
			if err != nil {
				return err
			}
			modifyReq.Header.Set("Content-Type", "application/json")

			modifyResp, err := orthancHTTPClient.Do(modifyReq)
			if err != nil {
				lastModifyErr = err
				slog.Warn("study modify network error, retrying...", "study_uuid", studyUUID, "attempt", attempt, "error", err)
				time.Sleep(backoff)
				backoff *= 2
				continue
			}

			respBody, err := io.ReadAll(modifyResp.Body)
			modifyResp.Body.Close()
			if err != nil {
				lastModifyErr = err
				slog.Warn("study modify failed to read body, retrying...", "study_uuid", studyUUID, "attempt", attempt, "error", err)
				time.Sleep(backoff)
				backoff *= 2
				continue
			}

			if modifyResp.StatusCode != http.StatusOK {
				lastModifyErr = fmt.Errorf("study modify failed with status %d: %s", modifyResp.StatusCode, string(respBody))
				if modifyResp.StatusCode >= 500 {
					slog.Warn("study modify server error, retrying...", "study_uuid", studyUUID, "attempt", attempt)
					time.Sleep(backoff)
					backoff *= 2
					continue
				}
				return lastModifyErr
			}

			if err := json.Unmarshal(respBody, &jobResp); err != nil {
				return fmt.Errorf("failed to parse study modify job response: %w", err)
			}
			lastModifyErr = nil
			break
		}

		if lastModifyErr != nil {
			return fmt.Errorf("failed to modify study %s after 5 attempts: %w", studyUUID, lastModifyErr)
		}

		slog.Info("Pre-emptive study modify job created", "study_uuid", studyUUID, "job_id", jobResp.ID)
		if err := waitForJobCompletion(config, jobResp.ID); err != nil {
			return fmt.Errorf("pre-emptive study modify job %s failed: %w", jobResp.ID, err)
		}
	}

	slog.Info("Pre-emptive demographic alignment completed successfully", "patient_id", patientID)
	return nil
}



// =============================================================================
// Study search utilities (used by the Java SIMRS front-end via Go API proxy)
// =============================================================================

// FindStudyByAccession queries Orthanc for a study matching the given AccessionNumber.
// Returns the Orthanc internal study ID, or empty string if not found.
func FindStudyByAccession(config *OrthancConfig, accessionNumber string) (string, error) {
	if !config.IsConfigured() {
		return "", fmt.Errorf("orthanc is not configured")
	}

	url := fmt.Sprintf("%s/tools/find", config.BaseURL())
	queryPayload := map[string]any{
		"Level":  "Study",
		"Expand": true,
		"Query": map[string]string{
			"AccessionNumber": accessionNumber,
		},
	}
	bodyBytes, err := json.Marshal(queryPayload)
	if err != nil {
		return "", err
	}

	maxRetries := 3
	backoff := 500 * time.Millisecond
	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := newOrthancRequest(http.MethodPost, url, bytes.NewReader(bodyBytes), config)
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := orthancHTTPClient.Do(req)
		if err != nil {
			slog.Warn("FindStudyByAccession network error, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			slog.Warn("FindStudyByAccession read error, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("find by ACSN failed with status %d: %s", resp.StatusCode, string(respBody))
		}

		var results []struct {
			ID string `json:"ID"`
		}
		if err := json.Unmarshal(respBody, &results); err != nil {
			return "", err
		}
		if len(results) > 0 {
			return results[0].ID, nil
		}
		return "", nil // not found (not an error)
	}

	return "", fmt.Errorf("failed to find study by ACSN after %d attempts: %w", maxRetries, err)
}

// FindAllPatientStudies queries Orthanc for ALL studies belonging to a PatientID (no date/modality filter).
// Returns the full expanded study list as a json.RawMessage for the Java proxy.
func FindAllPatientStudies(config *OrthancConfig, patientID string) (json.RawMessage, error) {
	if !config.IsConfigured() {
		return nil, fmt.Errorf("orthanc is not configured")
	}

	url := fmt.Sprintf("%s/tools/find", config.BaseURL())
	queryPayload := map[string]any{
		"Level":  "Study",
		"Expand": true,
		"Query": map[string]string{
			"PatientID": patientID,
		},
	}
	bodyBytes, err := json.Marshal(queryPayload)
	if err != nil {
		return nil, err
	}

	maxRetries := 3
	backoff := 500 * time.Millisecond
	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := newOrthancRequest(http.MethodPost, url, bytes.NewReader(bodyBytes), config)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := orthancHTTPClient.Do(req)
		if err != nil {
			slog.Warn("FindAllPatientStudies network error, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			slog.Warn("FindAllPatientStudies read error, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("find all patient studies failed with status %d: %s", resp.StatusCode, string(respBody))
		}

		return json.RawMessage(respBody), nil
	}

	return nil, fmt.Errorf("failed to find patient studies after %d attempts: %w", maxRetries, err)
}

// SendStudyToModality sends a study from Orthanc to a DICOM modality/AE Title.
// Wraps POST /modalities/{aeTitle}/store
func SendStudyToModality(config *OrthancConfig, studyID string, aeTitle string) error {
	if !config.IsConfigured() {
		return fmt.Errorf("orthanc is not configured")
	}

	url := fmt.Sprintf("%s/modalities/%s/store", config.BaseURL(), aeTitle)
	payload := []string{studyID}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	maxRetries := 3
	backoff := 1 * time.Second
	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := newOrthancRequest(http.MethodPost, url, bytes.NewReader(bodyBytes), config)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := orthancHTTPClient.Do(req)
		if err != nil {
			slog.Warn("SendStudyToModality network error, retrying...", "attempt", attempt, "error", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr := fmt.Errorf("send to modality failed with status %d: %s", resp.StatusCode, string(respBody))
			if attempt < maxRetries {
				slog.Warn("SendStudyToModality failed, retrying...", "attempt", attempt, "error", lastErr)
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return lastErr
		}

		slog.Info("Study sent to modality successfully", "study_id", studyID, "ae_title", aeTitle)
		return nil
	}

	return fmt.Errorf("failed to send study to modality after %d attempts", maxRetries)
}
