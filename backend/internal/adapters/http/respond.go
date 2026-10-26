// Package httpapi contains the HTTP layer: router, handlers and middleware.
// It lives in internal/http but is named httpapi to avoid clashing with net/http.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Error codes returned in {"error":{"code":...}}.
const (
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeInternal         = "internal_error"
	CodeDBUnavailable    = "db_unavailable"
	CodeShuttingDown     = "shutting_down"
	CodeGuestLimit       = "guest_limit"
	CodeDemoUnavailable  = "demo_unavailable"
	CodeInvalidJSON      = "invalid_json"
	CodeValidation       = "validation_error"
	CodeInvalidCreds     = "invalid_credentials"
	CodeEmailTaken       = "email_taken"
	CodeUnauthorized     = "unauthorized"
	CodeForbidden        = "forbidden"
	CodeMemberNotFound   = "member_not_found"
	CodeLastAdmin        = "last_admin"
	CodeVersionConflict  = "version_conflict"
	CodeNeighborsChanged = "neighbors_changed"
	CodeLabelExists      = "label_exists"
)

const maxBodyBytes = 1 << 20

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Fields carries per-field messages for validation errors.
	Fields map[string]string `json:"fields,omitempty"`
}

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json response", "err", err)
	}
}

// WriteError writes {"error":{"code":code,"message":message}} with the given status.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

// WriteValidationError writes a 422 with per-field messages in error.fields.
func WriteValidationError(w http.ResponseWriter, fields map[string]string) {
	WriteJSON(w, http.StatusUnprocessableEntity, errorBody{Error: errorDetail{
		Code:    CodeValidation,
		Message: "Revisa los datos enviados",
		Fields:  fields,
	}})
}

// internalError logs err with the request id and writes a generic 500.
func internalError(w http.ResponseWriter, r *http.Request, op string, err error) {
	slog.Error(op+" failed", "err", err, "request_id", RequestID(r.Context()))
	WriteError(w, http.StatusInternalServerError, CodeInternal, "internal server error")
}

// decodeJSON reads a JSON body of at most 1 MiB into dst. On failure it has
// already written a 400 and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "El cuerpo de la petición no es JSON válido")
		return false
	}
	return true
}
