package router

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"go-dcm/handler"
)

func SetupRouter() *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Route("/api/v1/convert", func(r chi.Router) {
		r.Post("/img2dcm", handler.HandleImg2Dcm)
		r.Post("/pdf2dcm", handler.HandlePdf2Dcm)
	})

	return r
}
