package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tradingengine/internal/gateway"
)

// Handler holds the gateway dependency for the HTTP layer.
type Handler struct {
	gw *gateway.Gateway
}

func NewHandler(gw *gateway.Gateway) *Handler {
	return &Handler{gw: gw}
}

// PlaceOrder handles POST /orders.
func (h *Handler) PlaceOrder(w http.ResponseWriter, r *http.Request) {
	var req gateway.PlaceOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// TODO: ClientId currently comes straight from the request body,
	// same as everything else, since there's no auth/session layer
	// yet to derive it from. Once auth exists this should come from
	// the authenticated context instead, not be client-supplied.

	resp, err := h.gw.PlaceOrder(req)
	if err != nil {
		writeGatewayError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

// GetOrder handles GET /orders/{id}.
func (h *Handler) GetOrder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	order, err := h.gw.GetOrder(id)
	if err != nil {
		writeGatewayError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, order)
}
func (h *Handler) CancelOrder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	clientID := r.URL.Query().Get("client_id")

	if clientID == "" {
		writeError(w, http.StatusBadRequest, "client_id is required")
		return
	}

	resp, err := h.gw.CancelOrder(gateway.CancelOrderRequest{
		OrderId:  id,
		ClientId: clientID,
	})
	if err != nil {
		writeGatewayError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetClientOrders handles GET /clients/{clientId}/orders.
func (h *Handler) GetClientOrders(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "clientId")

	orders, err := h.gw.GetClientOrders(clientID)
	if err != nil {
		writeGatewayError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, orders)
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
