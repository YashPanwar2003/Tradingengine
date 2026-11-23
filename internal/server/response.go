package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"tradingengine/pkg/types"
)

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

func writeGatewayError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, types.ErrOrderNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, types.ErrInvalidRequest),
		errors.Is(err, types.ErrInvalidQuantity),
		errors.Is(err, types.ErrInvalidPrice),
		errors.Is(err, types.ErrInvalidSymbol),
		errors.Is(err, types.ErrUnknownSide),
		errors.Is(err, types.ErrUnknownOrderType):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, types.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, types.ErrOrderAlreadyFilled),
		errors.Is(err, types.ErrOrderCancelled):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, types.ErrInsufficientFunds):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		writeError(w, http.StatusServiceUnavailable, "service busy, try again")
	default:
		log.Printf("internal error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
