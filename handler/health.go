package handler

import (
	"net/http"
	"runtime"

	"go-dcm/model"
	"go-dcm/service"
)

// AppVersion is set at build time or defaults to "dev".
var AppVersion = "dev"

// HandleHealth handles GET /health and GET /api/v1/health
func HandleHealth(w http.ResponseWriter, r *http.Request) {
	deps := make(map[string]string)

	// Check required DCMTK tools
	tools := []string{"img2dcm", "pdf2dcm", "cda2dcm", "stl2dcm", "dcmdump"}
	allOK := true
	for _, tool := range tools {
		if err := service.CheckToolAvailable(tool); err != nil {
			deps[tool] = "unavailable"
			allOK = false
		} else {
			deps[tool] = "available"
		}
	}

	deps["go_version"] = runtime.Version()

	status := "healthy"
	statusCode := http.StatusOK
	if !allOK {
		status = "degraded"
		statusCode = http.StatusServiceUnavailable
	}

	model.WriteJSON(w, statusCode, model.HealthResponse{
		Status:       status,
		Version:      AppVersion,
		Dependencies: deps,
	})
}
