// Package httperr centralizes how Victus logs and responds to server-side
// failures, so every handler reports internal errors the same way instead
// of hand-rolling log+http.Error pairs.
package httperr

import (
	"log/slog"
	"net/http"
)

// Internal logs err (with msg as context) at ERROR level and writes a
// generic 500 response — never leaking err's details to the client.
func Internal(w http.ResponseWriter, r *http.Request, msg string, err error, args ...any) {
	slog.ErrorContext(r.Context(), msg, append([]any{"error", err}, args...)...)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
