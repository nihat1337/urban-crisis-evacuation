package api

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"time"

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

func (s *Server) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = domain.NewID()
		}
		w.Header().Set("X-Request-ID", reqID)

		// CORS: the web app is served from the same origin, but the API is also
		// usable from a separate frontend (mobile app, другой домен).
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Dispatcher-Key, X-Lang, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")

		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "err", rec, "path", r.URL.Path, "req", reqID)
				s.writeError(sw, r, http.StatusInternalServerError, "internal")
			}
			if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/v1/events" {
				s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
					"ms", time.Since(start).Milliseconds(), "ip", clientIP(r), "req", reqID)
			}
		}()

		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/v1/events" {
			if !s.limiter.allow(clientIP(r)) {
				w.Header().Set("Retry-After", "1")
				s.writeError(sw, r, http.StatusTooManyRequests, "rate_limited")
				return
			}
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(sw, r.Body, 1<<20)
		}
		next.ServeHTTP(sw, r)
	})
}

// requireKey guards dispatcher endpoints with a shared secret
// (X-Dispatcher-Key header or Authorization: Bearer).
func (s *Server) requireKey(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Dispatcher-Key")
		if key == "" {
			if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				key = strings.TrimPrefix(auth, "Bearer ")
			}
		}
		if s.cfg.DispatcherKey == "" || subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.DispatcherKey)) != 1 {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	})
}

var _ = slog.Logger{}
