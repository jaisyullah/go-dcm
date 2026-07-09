package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"dicom-converter-api/model"
	"dicom-converter-api/service"
)

// HandleModifyStudy receives a JSON payload for Orthanc tags modification and forwards it to the service layer.
func HandleModifyStudy(w http.ResponseWriter, r *http.Request) {
	studyID := chi.URLParam(r, "id")
	if studyID == "" {
		model.WriteError(w, http.StatusBadRequest, "MISSING_STUDY_ID", "missing study id", "")
		return
	}

	var req service.OrthancModifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		model.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "invalid json payload", err.Error())
		return
	}

	config := service.LoadOrthancConfig()
	respBytes, err := service.ModifyStudy(&config, studyID, &req)
	if err != nil {
		model.WriteError(w, http.StatusInternalServerError, "MODIFY_FAILED", err.Error(), "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBytes)
}
