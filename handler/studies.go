package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"dicom-converter-api/model"
	"dicom-converter-api/service"
)

// HandleFindStudyByAccession handles POST /api/v1/studies/find-by-acsn
// Finds an Orthanc study by its AccessionNumber.
// Request: {"accession_number": "..."}
// Response: {"study_id": "..."} or {"study_id": ""} if not found
func HandleFindStudyByAccession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccessionNumber string `json:"accession_number"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		model.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Failed to parse JSON body", err.Error())
		return
	}
	if req.AccessionNumber == "" {
		model.WriteError(w, http.StatusBadRequest, "MISSING_ACSN", "accession_number is required", "")
		return
	}

	studyID, err := service.FindStudyByAccession(&OrthancCfg, req.AccessionNumber)
	if err != nil {
		model.WriteError(w, http.StatusInternalServerError, "ORTHANC_ERROR", err.Error(), "")
		return
	}

	model.WriteJSON(w, http.StatusOK, map[string]string{
		"study_id": studyID,
	})
}

// HandleFindPatientStudies handles POST /api/v1/patients/{id}/studies
// Returns ALL studies for a patient (no date/modality filter).
func HandleFindPatientStudies(w http.ResponseWriter, r *http.Request) {
	patientID := chi.URLParam(r, "id")
	if patientID == "" {
		model.WriteError(w, http.StatusBadRequest, "MISSING_PATIENT_ID", "Patient ID is required", "")
		return
	}

	result, err := service.FindAllPatientStudies(&OrthancCfg, patientID)
	if err != nil {
		model.WriteError(w, http.StatusInternalServerError, "ORTHANC_ERROR", err.Error(), "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result)
}

// HandleSendStudyToModality handles POST /api/v1/studies/{id}/send-to-modality/{ae}
// Sends a study from Orthanc to a DICOM modality.
func HandleSendStudyToModality(w http.ResponseWriter, r *http.Request) {
	studyID := chi.URLParam(r, "id")
	aeTitle := chi.URLParam(r, "ae")
	if studyID == "" || aeTitle == "" {
		model.WriteError(w, http.StatusBadRequest, "MISSING_PARAMS", "Study ID and AE Title are required", "")
		return
	}

	if err := service.SendStudyToModality(&OrthancCfg, studyID, aeTitle); err != nil {
		model.WriteError(w, http.StatusInternalServerError, "ORTHANC_ERROR", err.Error(), "")
		return
	}

	model.WriteJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": "Study sent to modality",
	})
}
