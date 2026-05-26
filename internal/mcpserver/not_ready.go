package mcpserver

import (
	"encoding/json"
	"net/http"
	"strings"
)

// NotReadyHandler returns an http.Handler that responds with 503 and a JSON
// body explaining how to bring the project online. Used when a configured
// project's runtime cannot be opened (e.g., storage schema missing).
func NotReadyHandler(slug string, cause error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		hint := "projectlens migrate --project " + slug
		body := map[string]string{
			"error":   "project not ready",
			"project": slug,
			"cause":   strings.TrimSpace(cause.Error()),
			"hint":    hint,
		}
		_ = json.NewEncoder(w).Encode(body)
	})
}
