package router

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"go-dcm/handler"
)

// SetupRouter configures all routes and middleware for the API.
func SetupRouter() *chi.Mux {
	r := chi.NewRouter()

	// Middleware stack
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(120 * time.Second))

	// CORS middleware for cross-origin requests
	r.Use(corsMiddleware)

	// Health endpoints
	r.Get("/health", handler.HandleHealth)

	// API v1 routes
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", handler.HandleHealth)

		r.Route("/convert", func(r chi.Router) {
			r.Post("/img2dcm", handler.HandleImg2Dcm)
			r.Post("/pdf2dcm", handler.HandlePdf2Dcm)
			r.Post("/cda2dcm", handler.HandleCda2Dcm)
			r.Post("/stl2dcm", handler.HandleStl2Dcm)
		})

		// Job status polling endpoint
		r.Get("/jobs/{id}", handler.HandleGetJob)

		// Convert & send directly to Orthanc
		r.Post("/send-to-orthanc", handler.HandleSendToOrthanc)
		r.Post("/send-to-orthanc-from-urls", handler.HandleSendToOrthancFromURLs)
	})

	return r
}

// corsMiddleware adds CORS headers for cross-origin requests.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
