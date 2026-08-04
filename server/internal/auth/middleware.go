package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type claimsContextKey struct{}

// ClaimsFrom returns the Claims stored by RequireUser, if any.
func ClaimsFrom(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(claimsContextKey{}).(Claims)
	return c, ok
}

// RequireUser validates a Bearer access JWT and injects Claims into the request context.
// Missing/invalid tokens yield 401 {"error":"..."}.
func RequireUser(a *Auth) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "missing or invalid authorization")
				return
			}
			claims, err := a.ParseAccessToken(raw, time.Now().UTC())
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}
			ctx := context.WithValue(r.Context(), claimsContextKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdmin wraps RequireUser and rejects non-admin roles with 403.
func RequireAdmin(a *Auth) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return RequireUser(a)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := ClaimsFrom(r.Context())
			if !ok || claims.Role != "admin" {
				writeJSONError(w, http.StatusForbidden, "admin required")
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	tok := strings.TrimSpace(header[len(prefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
