package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"dicom-converter-api/model"
	"dicom-converter-api/service"
)

// HandleGetJob returns the current status and result of a background job.
func HandleGetJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if jobID == "" {
		model.WriteError(w, http.StatusBadRequest, "MISSING_JOB_ID", "Job ID is required", "")
		return
	}

	job, exists := service.GetJob(jobID)
	if !exists {
		model.WriteError(w, http.StatusNotFound, "JOB_NOT_FOUND", "Job not found", "")
		return
	}

	model.WriteJSON(w, http.StatusOK, job)
}
