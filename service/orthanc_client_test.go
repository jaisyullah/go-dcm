package service

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestOrthancIntegration(t *testing.T) {
	config := LoadOrthancConfig()
	if !config.IsConfigured() {
		t.Skip("Orthanc is not configured, skipping integration test")
	}

	t.Run("FindStudyIDByUID and DeleteStudy", func(t *testing.T) {
		// Verify FindStudyIDByUID returns error for non-existent study UID
		_, err := FindStudyIDByUID(&config, "1.2.3.4.5.nonexistent")
		if err == nil {
			t.Error("expected error for non-existent study UID, got nil")
		}

		// Verify DeleteStudy returns error for non-existent study ID
		err = DeleteStudy(&config, "nonexistent-study-id")
		if err == nil {
			t.Error("expected error for deleting non-existent study, got nil")
		}
	})

	t.Run("cleanupDuplicateStudyOnFailure safety", func(t *testing.T) {
		// Mock a ModifyRequest
		req := &OrthancModifyRequest{
			Replace: map[string]any{
				"StudyInstanceUID": "1.2.3.4.5.original",
			},
		}

		// Call cleanup where target matches original. It should do nothing and return safely.
		cleanupDuplicateStudyOnFailure(&config, "original-study-id", req)
	})

	t.Run("cleanupDuplicateStudyOnFailure safety with real study", func(t *testing.T) {
		// Get all studies from Orthanc
		url := fmt.Sprintf("%s/studies", config.BaseURL())
		reqGet, err := newOrthancRequest("GET", url, nil, &config)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := orthancHTTPClient.Do(reqGet)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var studies []string
		var studiesDec struct {
			IDs []string
		}
		// Orthanc GET /studies returns either a list of IDs or a list of objects depending on options.
		// By default it returns a JSON array of strings.
		importJSON := json.NewDecoder(resp.Body)
		if err := importJSON.Decode(&studies); err != nil {
			// Try alternative format if any
			_ = importJSON.Decode(&studiesDec)
			studies = studiesDec.IDs
		}

		if len(studies) == 0 {
			t.Skip("No studies exist in Orthanc, skipping real study safety check")
		}

		// Get the first study's UID
		studyID := studies[0]
		studyUrl := fmt.Sprintf("%s/studies/%s", config.BaseURL(), studyID)
		reqStudy, err := newOrthancRequest("GET", studyUrl, nil, &config)
		if err != nil {
			t.Fatal(err)
		}
		respStudy, err := orthancHTTPClient.Do(reqStudy)
		if err != nil {
			t.Fatal(err)
		}
		defer respStudy.Body.Close()

		var studyDetails struct {
			MainDicomTags struct {
				StudyInstanceUID string `json:"StudyInstanceUID"`
			} `json:"MainDicomTags"`
		}
		if err := json.NewDecoder(respStudy.Body).Decode(&studyDetails); err != nil {
			t.Fatal(err)
		}

		studyUID := studyDetails.MainDicomTags.StudyInstanceUID
		if studyUID == "" {
			t.Skip("Existing study has no UID, skipping safety check")
		}

		// Mock ModifyRequest with this real study's UID
		req := &OrthancModifyRequest{
			Replace: map[string]any{
				"StudyInstanceUID": studyUID,
			},
		}

		// Call cleanup passing the same studyID. It must skip deleting because targetStudyID == originalStudyID!
		cleanupDuplicateStudyOnFailure(&config, studyID, req)

		// Verify that the study STILL exists in Orthanc (it was NOT deleted!)
		respVerify, err := orthancHTTPClient.Do(reqStudy)
		if err != nil {
			t.Fatal(err)
		}
		defer respVerify.Body.Close()
		if respVerify.StatusCode != 200 {
			t.Errorf("study was incorrectly deleted during safety check! status: %d", respVerify.StatusCode)
		}
	})

	t.Run("ModifyStudy and DeleteStudy success", func(t *testing.T) {
		// Get all studies from Orthanc
		url := fmt.Sprintf("%s/studies", config.BaseURL())
		reqGet, err := newOrthancRequest("GET", url, nil, &config)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := orthancHTTPClient.Do(reqGet)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var studies []string
		var studiesDec struct {
			IDs []string
		}
		importJSON := json.NewDecoder(resp.Body)
		if err := importJSON.Decode(&studies); err != nil {
			_ = importJSON.Decode(&studiesDec)
			studies = studiesDec.IDs
		}

		if len(studies) == 0 {
			t.Skip("No studies exist in Orthanc, skipping modification test")
		}

		originalStudyID := studies[0]
		newStudyUID := "2.25.999999999999999999999999"

		// Call ModifyStudy to create a new study (using KeepSource: true to keep the original)
		modifyReq := &OrthancModifyRequest{
			Replace: map[string]any{
				"StudyInstanceUID": newStudyUID,
			},
			KeepSource: true,
			Force:      true,
		}

		respBytes, err := ModifyStudy(&config, originalStudyID, modifyReq)
		if err != nil {
			t.Fatalf("ModifyStudy failed: %v", err)
		}

		var modifyResp struct {
			ID string `json:"ID"`
		}
		if err := json.Unmarshal(respBytes, &modifyResp); err != nil {
			t.Fatal(err)
		}

		newStudyID := modifyResp.ID
		if newStudyID == "" || newStudyID == originalStudyID {
			t.Fatalf("expected new study ID to be generated, got %s", newStudyID)
		}

		// Verify that the new study now exists in Orthanc by querying by UID
		foundID, err := FindStudyIDByUID(&config, newStudyUID)
		if err != nil {
			t.Fatalf("failed to find new study by UID: %v", err)
		}
		if foundID != newStudyID {
			t.Errorf("expected found ID to be %s, got %s", newStudyID, foundID)
		}

		// Clean up the new study using DeleteStudy
		err = DeleteStudy(&config, newStudyID)
		if err != nil {
			t.Fatalf("failed to delete new study: %v", err)
		}

		// Verify it is deleted
		_, err = FindStudyIDByUID(&config, newStudyUID)
		if err == nil {
			t.Error("expected new study to be deleted, but it was found")
		}
	})
}
