package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// intParam reads an optional positive integer query parameter, clamped to max.
func intParam(r *http.Request, name string, def, max int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	if n > max {
		n = max
	}
	return n, nil
}

// GetBook handles GET /books/{symbol}?depth=20
func (h *Handler) GetBook(w http.ResponseWriter, r *http.Request) {
	depth, err := intParam(r, "depth", 20, 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	snap, err := h.gw.GetBook(r.Context(), chi.URLParam(r, "symbol"), depth)
	if err != nil {
		writeGatewayError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// ListTrades handles GET /trades?symbol=BTC-USD&limit=50
func (h *Handler) ListTrades(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeError(w, http.StatusBadRequest, "symbol is required")
		return
	}
	limit, err := intParam(r, "limit", 50, 200)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	trades, err := h.gw.ListTrades(r.Context(), symbol, limit)
	if err != nil {
		writeGatewayError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, trades)
}

// GetOrderTrades handles GET /orders/{id}/trades
func (h *Handler) GetOrderTrades(w http.ResponseWriter, r *http.Request) {
	trades, err := h.gw.GetOrderTrades(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeGatewayError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, trades)
}

// Ready handles GET /ready: 200 only when Postgres answers.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.gw.Ready(ctx); err != nil {
		log.Printf("readiness check failed: %v", err) // details stay in the log, not the response
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "postgres": "down"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "postgres": "up"})
}
