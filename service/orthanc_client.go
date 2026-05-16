package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
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

// UploadInstance uploads a DICOM file to Orthanc via POST /instances.
// Orthanc expects the raw DICOM binary as the request body.
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

	req, err := newOrthancRequest(http.MethodPost, url, bytes.NewReader(dcmData), config)
	if err != nil {
		return nil, fmt.Errorf("failed to create upload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/dicom")

	slog.Info("uploading DICOM instance to Orthanc", "url", url, "file_size", len(dcmData))

	resp, err := orthancHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to upload to Orthanc: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Orthanc upload response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		slog.Error("Orthanc upload failed",
			"status", resp.StatusCode,
			"body", string(respBody),
		)
		return nil, fmt.Errorf("Orthanc upload failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var uploadResp OrthancUploadResponse
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

// ModifyStudy modifies DICOM tags on a study via POST /studies/{id}/modify.
// Forces Synchronous=true for production reliability.
func ModifyStudy(config *OrthancConfig, studyID string, modifyReq *OrthancModifyRequest) (json.RawMessage, error) {
	if !config.IsConfigured() {
		return nil, fmt.Errorf("orthanc is not configured (ORTHANC_URL is empty)")
	}

	// Force synchronous for reliable response
	modifyReq.Synchronous = true

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
		"replace_tag_count", len(modifyReq.Replace),
		"replace_tags", modifyReq.Replace,
	)

	resp, err := orthancHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send modify request to Orthanc: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Orthanc modify response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		slog.Error("Orthanc modify failed",
			"status", resp.StatusCode,
			"body", string(respBody),
		)
		return nil, fmt.Errorf("Orthanc modify failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	slog.Info("study tags modified successfully in Orthanc", "study_id", studyID)

	return json.RawMessage(respBody), nil
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
