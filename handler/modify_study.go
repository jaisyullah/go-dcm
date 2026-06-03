package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"go-dcm/service"
)

// HandleModifyStudy receives a JSON payload for Orthanc tags modification and forwards it to the service layer.
func HandleModifyStudy(w http.ResponseWriter, r *http.Request) {
	studyID := chi.URLParam(r, "id")
	if studyID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": "missing study id",
		})
		return
	}

	var req service.OrthancModifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": "invalid json payload: " + err.Error(),
		})
		return
	}

	config := service.LoadOrthancConfig()
	respBytes, err := service.ModifyStudy(&config, studyID, &req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBytes)
}
