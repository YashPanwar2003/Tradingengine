package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"tradingengine/internal/gateway"
)

func NewRouter(gw *gateway.Gateway, apiKey string) http.Handler {
	h := NewHandler(gw)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(5 * time.Second))

	r.Get("/health", h.HealthCheck) // liveness: the process is up
	r.Get("/ready", h.Ready)        // readiness: Postgres answers

	r.Group(func(r chi.Router) {
		r.Use(RequireKey(apiKey))

		r.Route("/orders", func(r chi.Router) {
			r.Post("/", h.PlaceOrder)
			r.Get("/{id}", h.GetOrder)
			r.Get("/{id}/trades", h.GetOrderTrades)
			r.Delete("/{id}", h.CancelOrder)
		})

		r.Get("/clients/{clientId}/orders", h.GetClientOrders)
		r.Get("/trades", h.ListTrades)
		r.Get("/books/{symbol}", h.GetBook)
	})

	return r
}
