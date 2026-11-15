package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/auth"
	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Flush keeps SSE working through the wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type ctxKey int

const ctxClaims ctxKey = iota

func (s *Server) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = domain.NewID()
		}
		w.Header().Set("X-Request-ID", reqID)

		// CORS: the web app is served from the same origin, but the API is also
		// usable from a separate frontend (mobile app, another domain).
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Dispatcher-Key, X-Lang, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		// Origin only (not "no-referrer"): OSM tile servers block requests without
		// a Referer, and referrer-restricted Google Maps keys need it too.
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		isAPI := strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/v1/events"
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "err", rec, "path", r.URL.Path, "req", reqID)
				s.writeError(sw, r, http.StatusInternalServerError, "internal")
			}
			if isAPI {
				d := time.Since(start)
				s.metrics.record(r.URL.Path, sw.status, d)
				// Under a flood, one log line per bot request would itself become a
				// cost; keep full logging for non-2xx and sample the rest.
				if sw.status >= 400 || r.URL.Path != "/api/v1/sos" || s.metrics.total%50 == 0 {
					s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
						"ms", d.Milliseconds(), "ip", s.clientIP(r), "req", reqID)
				}
			}
		}()

		if isAPI && !s.limiter.allow(s.clientIP(r)) {
			w.Header().Set("Retry-After", "1")
			s.writeError(sw, r, http.StatusTooManyRequests, "rate_limited")
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(sw, r.Body, 1<<20)
		}
		next.ServeHTTP(sw, r)
	})
}

// clientIP honours X-Forwarded-For only when TrustProxy is on (behind a
// reverse proxy); otherwise bots cannot spoof their way past the IP limiter.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	return remoteIP(r)
}

// bearer extracts the credential from Authorization: Bearer or X-Dispatcher-Key.
func bearer(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return r.Header.Get("X-Dispatcher-Key")
}

// authenticate resolves the caller's role: a JWT issued by /auth/login, or the
// shared service key (treated as admin, for automation/curl).
func (s *Server) authenticate(r *http.Request) (auth.Claims, bool) {
	cred := bearer(r)
	if cred == "" {
		return auth.Claims{}, false
	}
	if len(s.cfg.JWTSecret) > 0 {
		if c, err := auth.Verify(s.cfg.JWTSecret, cred); err == nil {
			return c, true
		}
	}
	if s.cfg.DispatcherKey != "" && subtle.ConstantTimeCompare([]byte(cred), []byte(s.cfg.DispatcherKey)) == 1 {
		return auth.Claims{Sub: "service-key", Role: auth.RoleAdmin}, true
	}
	return auth.Claims{}, false
}

// requireRole guards dispatcher endpoints: 401 without a valid credential,
// 403 when the role is below the required one (RBAC).
func (s *Server) requireRole(min string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := s.authenticate(r)
		if !ok {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !auth.AtLeast(c.Role, min) {
			s.log.Warn("rbac denied", "user", c.Sub, "role", c.Role, "need", min, "path", r.URL.Path)
			s.writeError(w, r, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxClaims, c)))
	})
}

// requireKey is kept as an alias for admin-only routes.
func (s *Server) requireKey(next http.HandlerFunc) http.Handler { return s.requireRole(auth.RoleAdmin, next) }

func claimsFrom(r *http.Request) auth.Claims {
	c, _ := r.Context().Value(ctxClaims).(auth.Claims)
	return c
}
