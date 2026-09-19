package bans

import "github.com/go-chi/chi/v5"

func RegisterRoutes(r chi.Router, h *Handler) {
	r.Get("/bans", h.ListBans)
	r.Post("/bans/{id}/release", h.ReleaseBan)
}
