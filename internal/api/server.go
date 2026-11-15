// Package api exposes the HTTP surface: JSON API v1, Server-Sent Events and
// the embedded web apps (citizen + dispatcher).
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
	"github.com/nihat1337/urban-crisis-evacuation/internal/events"
	"github.com/nihat1337/urban-crisis-evacuation/internal/i18n"
	"github.com/nihat1337/urban-crisis-evacuation/internal/service"
	"github.com/nihat1337/urban-crisis-evacuation/internal/store"
	"github.com/nihat1337/urban-crisis-evacuation/web"
)

type Config struct {
	DispatcherKey  string
	RateLimitRPS   float64
	RateLimitBurst int
	DefaultLang    string
	Version        string
	GoogleMapsKey  string // browser (referrer-restricted) key; empty → OSM tiles
}

type Server struct {
	cfg     Config
	svc     *service.Service
	hub     *events.Hub
	log     *slog.Logger
	limiter *limiter
	started time.Time
}

func New(cfg Config, svc *service.Service, hub *events.Hub, log *slog.Logger) http.Handler {
	s := &Server{cfg: cfg, svc: svc, hub: hub, log: log, limiter: newLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst), started: time.Now()}
	mux := http.NewServeMux()

	// Web apps
	mux.HandleFunc("GET /{$}", s.page("citizen.html"))
	mux.HandleFunc("GET /dispatcher", s.page("dispatcher.html"))
	static, _ := fs.Sub(web.FS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	// Module 1 — citizen (public)
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/config", s.clientConfig)
	mux.HandleFunc("GET /api/v1/i18n", s.languages)
	mux.HandleFunc("GET /api/v1/i18n/{lang}", s.bundle)
	mux.HandleFunc("GET /api/v1/zones", s.zones)
	mux.HandleFunc("GET /api/v1/shelters", s.shelters)
	mux.HandleFunc("GET /api/v1/evacuation/plan", s.plan)
	mux.HandleFunc("POST /api/v1/sos", s.createSOS)
	mux.HandleFunc("POST /api/v1/citizens/{id}/status", s.citizenStatus)
	mux.HandleFunc("GET /api/v1/citizens/{id}", s.getCitizen)
	mux.HandleFunc("PUT /api/v1/citizens/{id}/profile", s.putProfile)
	mux.HandleFunc("GET /api/v1/roads", s.roads)
	mux.Handle("GET /api/v1/events", hub)

	// Module 2 — dispatcher (protected)
	mux.Handle("GET /api/v1/dispatch/session", s.requireKey(s.session))
	mux.Handle("GET /api/v1/dispatch/overview", s.requireKey(s.overview))
	mux.Handle("GET /api/v1/dispatch/sos", s.requireKey(s.listSOS))
	mux.Handle("POST /api/v1/dispatch/sos/{id}/resolve", s.requireKey(s.resolveSOS))
	mux.Handle("POST /api/v1/dispatch/sos/{id}/notify", s.requireKey(s.notifySOS))
	mux.Handle("GET /api/v1/dispatch/notifications", s.requireKey(s.listNotifications))
	mux.Handle("PATCH /api/v1/dispatch/shelters/{id}", s.requireKey(s.patchShelter))
	mux.Handle("PATCH /api/v1/dispatch/roads/{id}", s.requireKey(s.patchRoad))
	mux.Handle("PATCH /api/v1/dispatch/brigades/{id}", s.requireKey(s.patchBrigade))
	mux.Handle("POST /api/v1/dispatch/alerts/{id}/execute", s.requireKey(s.executeAlert))

	return s.wrap(mux)
}

// --- helpers ---

func (s *Server) lang(r *http.Request) string {
	explicit := r.URL.Query().Get("lang")
	if explicit == "" {
		explicit = r.Header.Get("X-Lang")
	}
	return i18n.Pick(explicit, r.Header.Get("Accept-Language"), s.cfg.DefaultLang)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code":    code,
		"message": i18n.T(s.lang(r), "err."+code),
	}})
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var se *service.Error
	switch {
	case errors.As(err, &se):
		s.writeError(w, r, se.Status, se.Code)
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, r, http.StatusNotFound, "not_found")
	default:
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			s.writeError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large")
			return
		}
		s.log.Error("request failed", "path", r.URL.Path, "err", err)
		s.writeError(w, r, http.StatusInternalServerError, "internal")
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return err
		}
		return &service.Error{Status: 400, Code: "invalid_json", Err: err}
	}
	return nil
}

// queryAccessible is true for ?accessible=true|1 or ?role=priority.
func queryAccessible(r *http.Request) bool {
	q := r.URL.Query()
	if q.Get("role") == string(domain.RolePriority) {
		return true
	}
	v, _ := strconv.ParseBool(q.Get("accessible"))
	return v
}

func queryPoint(r *http.Request) *domain.Point {
	q := r.URL.Query()
	lat, err1 := strconv.ParseFloat(q.Get("lat"), 64)
	lng, err2 := strconv.ParseFloat(q.Get("lng"), 64)
	if err1 != nil || err2 != nil {
		return nil
	}
	return &domain.Point{Lat: lat, Lng: lng}
}

func (s *Server) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := web.FS.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	}
}

// --- public handlers ---

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	code := http.StatusOK
	dbErr := ""
	if err := s.svc.Ping(r.Context()); err != nil {
		status, code, dbErr = "degraded", http.StatusServiceUnavailable, err.Error()
	}
	writeJSON(w, code, map[string]any{
		"status":    status,
		"version":   s.cfg.Version,
		"uptime_s":  int(time.Since(s.started).Seconds()),
		"store":     s.svc.StoreName(),
		"store_err": dbErr,
		"routing":   s.svc.RoutingStatus(),
		"sse_subscribers": s.hub.Subscribers(),
		"time":      time.Now().UTC(),
	})
}

// clientConfig tells the web apps which map provider to use. The Google key
// is a browser key restricted by HTTP referrer, so exposing it here is the
// intended usage; without a key the apps render OpenStreetMap tiles.
func (s *Server) clientConfig(w http.ResponseWriter, _ *http.Request) {
	provider := "osm"
	if s.cfg.GoogleMapsKey != "" {
		provider = "google"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"map_provider":    provider,
		"google_maps_key": s.cfg.GoogleMapsKey,
		"routing":         s.svc.RoutingStatus(),
		"default_lang":    s.cfg.DefaultLang,
		"version":         s.cfg.Version,
	})
}

func (s *Server) languages(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"languages": i18n.Languages(), "default": s.cfg.DefaultLang})
}

func (s *Server) bundle(w http.ResponseWriter, r *http.Request) {
	b, ok := i18n.Bundle(r.PathValue("lang"))
	if !ok {
		s.writeError(w, r, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) zones(w http.ResponseWriter, r *http.Request) {
	zones, err := s.svc.Zones(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"zones": zones})
}

func (s *Server) roads(w http.ResponseWriter, r *http.Request) {
	roads, err := s.svc.Roads(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roads": roads, "generated_at": time.Now().UTC()})
}

func (s *Server) shelters(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	acc := queryAccessible(r)
	opts, err := s.svc.NearestShelters(r.Context(), queryPoint(r), r.URL.Query().Get("zone"), limit, acc)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shelters": opts, "accessible": acc})
}

func (s *Server) plan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.svc.Plan(r.Context(), queryPoint(r), r.URL.Query().Get("zone"), queryAccessible(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) createSOS(w http.ResponseWriter, r *http.Request) {
	var in service.SOSInput
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	res, err := s.svc.SubmitSOS(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func (s *Server) getCitizen(w http.ResponseWriter, r *http.Request) {
	c, err := s.svc.GetCitizen(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"citizen": c})
}

func (s *Server) putProfile(w http.ResponseWriter, r *http.Request) {
	var in service.ProfileInput
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	c, err := s.svc.UpsertProfile(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"citizen": c})
}

func (s *Server) notifySOS(w http.ResponseWriter, r *http.Request) {
	n, err := s.svc.NotifySOS(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notification": n})
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.svc.ListNotifications(r.Context(), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": list})
}

func (s *Server) citizenStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status domain.CitizenStatus `json:"status"`
		Role   domain.CitizenRole   `json:"role"`
		Lat    *float64             `json:"lat"`
		Lng    *float64             `json:"lng"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	var loc *domain.Point
	if in.Lat != nil && in.Lng != nil {
		loc = &domain.Point{Lat: *in.Lat, Lng: *in.Lng}
	}
	c, err := s.svc.UpdateCitizenStatus(r.Context(), r.PathValue("id"), in.Status, in.Role, loc)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"citizen": c})
}

// --- dispatcher handlers ---

// session lets the dispatcher UI validate a key at login without pulling the
// whole overview. requireKey already rejected bad keys with 401.
func (s *Server) session(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"role": "dispatcher", "authenticated": true, "time": time.Now().UTC()})
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ov, err := s.svc.Overview(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ov)
}

func (s *Server) listSOS(w http.ResponseWriter, r *http.Request) {
	minutes, _ := strconv.Atoi(r.URL.Query().Get("since_minutes"))
	list, err := s.svc.ListSOS(r.Context(), time.Duration(minutes)*time.Minute)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sos": list})
}

func (s *Server) resolveSOS(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.ResolveSOS(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sos": v})
}

func (s *Server) patchShelter(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Occupancy *int  `json:"occupancy"`
		Active    *bool `json:"active"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	sh, alert, err := s.svc.SetShelter(r.Context(), r.PathValue("id"), in.Occupancy, in.Active)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shelter": sh, "occupancy_pct": sh.OccupancyPct(), "alert": alert})
}

func (s *Server) patchRoad(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status domain.RoadStatus `json:"status"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	road, err := s.svc.SetRoadStatus(r.Context(), r.PathValue("id"), in.Status)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"road": road})
}

func (s *Server) patchBrigade(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status *domain.BrigadeStatus `json:"status"`
		Lat    *float64              `json:"lat"`
		Lng    *float64              `json:"lng"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	var loc *domain.Point
	if in.Lat != nil && in.Lng != nil {
		loc = &domain.Point{Lat: *in.Lat, Lng: *in.Lng}
	}
	b, err := s.svc.UpdateBrigade(r.Context(), r.PathValue("id"), in.Status, loc)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"brigade": b})
}

func (s *Server) executeAlert(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action domain.AlertAction `json:"action"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	res, err := s.svc.ExecuteAlert(r.Context(), r.PathValue("id"), in.Action)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
