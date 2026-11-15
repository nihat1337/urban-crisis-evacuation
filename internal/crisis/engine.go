// Package crisis implements the "Kritik Böhran" algorithm:
//
//   - ≥ MinSignals distinct SOS signals from the same zone or within RadiusM of
//     each other inside Window  → RED alert "əlavə xilasetmə briqadası göndər"
//   - a shelter above ShelterAlertPct  → RED alert "yeni toplanış nöqtəsi aç"
//
// Every threshold is configuration so the jury can change the rules live.
package crisis

import (
	"context"
	"fmt"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
	"github.com/nihat1337/urban-crisis-evacuation/internal/store"
)

type Config struct {
	Window          time.Duration `json:"window"`
	RadiusM         float64       `json:"radius_m"`
	MinSignals      int           `json:"min_signals"`
	ShelterAlertPct float64       `json:"shelter_alert_pct"`
}

func DefaultConfig() Config {
	return Config{Window: 5 * time.Minute, RadiusM: 500, MinSignals: 4, ShelterAlertPct: 95}
}

type Engine struct {
	cfg   Config
	store store.Store
	Now   func() time.Time
}

func New(cfg Config, st store.Store) *Engine {
	return &Engine{cfg: cfg, store: st, Now: func() time.Time { return time.Now().UTC() }}
}

func (e *Engine) Config() Config { return e.cfg }

// OnSOS evaluates the cluster rule after a new signal has been stored.
// It returns the created alert, or nil when no new alert is warranted.
func (e *Engine) OnSOS(ctx context.Context, s domain.SOS) (*domain.Alert, error) {
	now := e.Now()
	recent, err := e.store.ListSOS(ctx, now.Add(-e.cfg.Window))
	if err != nil {
		return nil, err
	}
	// Count distinct citizens (fərqli SOS) that are either in the same zone or within the radius.
	citizens := map[string]bool{}
	for _, r := range recent {
		if r.Status == domain.SOSResolved {
			continue
		}
		if r.ZoneID == s.ZoneID || domain.DistanceM(r.Location, s.Location) <= e.cfg.RadiusM {
			citizens[r.CitizenID] = true
		}
	}
	citizens[s.CitizenID] = true
	if len(citizens) < e.cfg.MinSignals {
		return nil, nil
	}
	// Deduplicate: one open cluster alert per zone per window.
	alerts, err := e.store.ListAlerts(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range alerts {
		if a.Kind == domain.AlertSOSCluster && a.ZoneID == s.ZoneID && !a.Executed && now.Sub(a.CreatedAt) < e.cfg.Window {
			return nil, nil
		}
	}
	alert := domain.Alert{
		ID:          domain.NewID(),
		Level:       domain.AlertRed,
		Kind:        domain.AlertSOSCluster,
		ZoneID:      s.ZoneID,
		Center:      s.Location,
		SignalCount: len(citizens),
		Message: fmt.Sprintf("QIRMIZI HƏYƏCAN: %d fərqli SOS siqnalı %d dəqiqə ərzində (%s zonası / %.0f m radius). Əlavə xilasetmə briqadası göndərin.",
			len(citizens), int(e.cfg.Window.Minutes()), s.ZoneID, e.cfg.RadiusM),
		SuggestedActions: []domain.AlertAction{domain.ActionDispatchBrigade, domain.ActionOpenShelter},
		CreatedAt:        now,
	}
	if err := e.store.UpsertAlert(ctx, alert); err != nil {
		return nil, err
	}
	return &alert, nil
}

// OnShelter evaluates the occupancy rule after a shelter update.
func (e *Engine) OnShelter(ctx context.Context, sh domain.Shelter) (*domain.Alert, error) {
	if !sh.Active || sh.OccupancyPct() <= e.cfg.ShelterAlertPct {
		return nil, nil
	}
	alerts, err := e.store.ListAlerts(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range alerts {
		if a.Kind == domain.AlertShelterFull && a.ShelterID == sh.ID && !a.Executed {
			return nil, nil
		}
	}
	alert := domain.Alert{
		ID:        domain.NewID(),
		Level:     domain.AlertRed,
		Kind:      domain.AlertShelterFull,
		ZoneID:    sh.ZoneID,
		ShelterID: sh.ID,
		Center:    sh.Location,
		Message: fmt.Sprintf("QIRMIZI HƏYƏCAN: «%s» doluluğu %.1f%% (hədd %.0f%%). Yeni toplanış nöqtəsi açın.",
			sh.Name, sh.OccupancyPct(), e.cfg.ShelterAlertPct),
		SuggestedActions: []domain.AlertAction{domain.ActionOpenShelter, domain.ActionDispatchBrigade},
		CreatedAt:        e.Now(),
	}
	if err := e.store.UpsertAlert(ctx, alert); err != nil {
		return nil, err
	}
	return &alert, nil
}
