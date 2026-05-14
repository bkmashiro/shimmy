package server

import (
	"net/http"
	"strings"
)

// NormalizePath rewrites request paths that end in /evaluate or /evaluate/health
// to their canonical forms before the mux routes them. This mirrors the Python
// BaseEvaluationFunctionLayer behaviour: API Gateway forwards the full
// function-name prefix (e.g. /compareSets/evaluate), but shimmy only registers
// /evaluate.
func NormalizePath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/evaluate/health"):
			r = r.Clone(r.Context())
			r.URL.Path = "/evaluate/health"
		case strings.HasSuffix(r.URL.Path, "/evaluate"):
			r = r.Clone(r.Context())
			r.URL.Path = "/evaluate"
		}
		next.ServeHTTP(w, r)
	})
}
