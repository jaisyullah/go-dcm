package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"dicom-converter-api/handler"
	"dicom-converter-api/router"
	"dicom-converter-api/service"
)

func main() {
	// Initialize structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Load configuration from environment
	port := getEnv("PORT", "8080")
	loadUploadLimits()
	loadOrthancConfig()

	// Initialize background worker pool
	service.InitWorkerPool()

	// Setup router
	r := router.SetupRouter()

	// Create HTTP server
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		slog.Info("starting dicom-converter-api server",
			"port", port,
			"version", handler.AppVersion,
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed to start", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	slog.Info("received shutdown signal", "signal", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}

	slog.Info("server stopped gracefully")
}

// getEnv returns the value of an environment variable or a default.
func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// loadUploadLimits configures upload size limits from environment variables.
func loadUploadLimits() {
	if v := os.Getenv("MAX_IMAGE_UPLOAD_MB"); v != "" {
		if mb, err := strconv.ParseInt(v, 10, 64); err == nil && mb > 0 {
			handler.MaxImageUploadSize = mb << 20
			slog.Info("configured image upload limit", "max_mb", mb)
		}
	}
	if v := os.Getenv("MAX_PDF_UPLOAD_MB"); v != "" {
		if mb, err := strconv.ParseInt(v, 10, 64); err == nil && mb > 0 {
			handler.MaxPDFUploadSize = mb << 20
			slog.Info("configured PDF upload limit", "max_mb", mb)
		}
	}
	if v := os.Getenv("MAX_CDA_UPLOAD_MB"); v != "" {
		if mb, err := strconv.ParseInt(v, 10, 64); err == nil && mb > 0 {
			handler.MaxCDAUploadSize = mb << 20
			slog.Info("configured CDA upload limit", "max_mb", mb)
		}
	}
	if v := os.Getenv("MAX_STL_UPLOAD_MB"); v != "" {
		if mb, err := strconv.ParseInt(v, 10, 64); err == nil && mb > 0 {
			handler.MaxSTLUploadSize = mb << 20
			slog.Info("configured STL upload limit", "max_mb", mb)
		}
	}
}

// loadOrthancConfig loads Orthanc connection settings from environment variables.
func loadOrthancConfig() {
	cfg := service.LoadOrthancConfig()
	handler.OrthancCfg = cfg

	if cfg.IsConfigured() {
		authStatus := "disabled"
		if cfg.User != "" {
			authStatus = "enabled (user: " + cfg.User + ")"
		}
		slog.Info("Orthanc configured",
			"url", cfg.BaseURL(),
			"auth", authStatus,
		)
	} else {
		slog.Warn("Orthanc not configured — send-to-orthanc endpoint will be unavailable. Set ORTHANC_URL to enable.")
	}
}
