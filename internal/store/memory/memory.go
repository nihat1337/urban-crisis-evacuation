// Package memory is an in-process Store used for tests, local development and
// as an automatic fallback when the database is unreachable.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
	"github.com/nihat1337/urban-crisis-evacuation/internal/store"
)

type Store struct {
	mu       sync.RWMutex
	zones    map[string]domain.Zone
	shelters map[string]domain.Shelter
	sos      map[string]domain.SOS
	citizens map[string]domain.Citizen
	brigades map[string]domain.Brigade
	roads    map[string]domain.Road
	alerts   map[string]domain.Alert
	notifs   []domain.Notification
}

var _ store.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		zones:    map[string]domain.Zone{},
		shelters: map[string]domain.Shelter{},
		sos:      map[string]domain.SOS{},
		citizens: map[string]domain.Citizen{},
		brigades: map[string]domain.Brigade{},
		roads:    map[string]domain.Road{},
		alerts:   map[string]domain.Alert{},
	}
}

func (s *Store) Name() string                   { return "memory" }
func (s *Store) Ping(context.Context) error     { return nil }
func (s *Store) Close()                         {}

func sortedValues[T any](m map[string]T, less func(a, b T) bool) []T {
	out := make([]T, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

func (s *Store) ListZones(context.Context) ([]domain.Zone, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.zones, func(a, b domain.Zone) bool { return a.Name < b.Name }), nil
}

func (s *Store) UpsertZone(_ context.Context, z domain.Zone) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.zones[z.ID] = z
	return nil
}

func (s *Store) ListShelters(context.Context) ([]domain.Shelter, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.shelters, func(a, b domain.Shelter) bool { return a.ID < b.ID }), nil
}

func (s *Store) GetShelter(_ context.Context, id string) (domain.Shelter, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.shelters[id]
	if !ok {
		return domain.Shelter{}, store.ErrNotFound
	}
	return v, nil
}

func (s *Store) UpsertShelter(_ context.Context, sh domain.Shelter) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shelters[sh.ID] = sh
	return nil
}

func (s *Store) CreateSOS(_ context.Context, v domain.SOS) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sos[v.ID] = v
	return nil
}

func (s *Store) UpdateSOS(_ context.Context, v domain.SOS) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sos[v.ID]; !ok {
		return store.ErrNotFound
	}
	s.sos[v.ID] = v
	return nil
}

func (s *Store) GetSOS(_ context.Context, id string) (domain.SOS, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.sos[id]
	if !ok {
		return domain.SOS{}, store.ErrNotFound
	}
	return v, nil
}

func (s *Store) ListSOS(_ context.Context, since time.Time) ([]domain.SOS, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.SOS, 0, len(s.sos))
	for _, v := range s.sos {
		if since.IsZero() || !v.CreatedAt.Before(since) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority // critical (1) first
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *Store) UpsertCitizen(_ context.Context, c domain.Citizen) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.citizens[c.ID] = c
	return nil
}

func (s *Store) GetCitizen(_ context.Context, id string) (domain.Citizen, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.citizens[id]
	if !ok {
		return domain.Citizen{}, store.ErrNotFound
	}
	return v, nil
}

func (s *Store) CreateNotification(_ context.Context, n domain.Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifs = append(s.notifs, n)
	if len(s.notifs) > 1000 {
		s.notifs = s.notifs[len(s.notifs)-1000:]
	}
	return nil
}

func (s *Store) ListNotifications(_ context.Context, limit int) ([]domain.Notification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	out := make([]domain.Notification, 0, limit)
	for i := len(s.notifs) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.notifs[i])
	}
	return out, nil
}

func (s *Store) ListCitizens(context.Context) ([]domain.Citizen, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.citizens, func(a, b domain.Citizen) bool { return a.UpdatedAt.After(b.UpdatedAt) }), nil
}

func (s *Store) ListBrigades(context.Context) ([]domain.Brigade, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.brigades, func(a, b domain.Brigade) bool { return a.ID < b.ID }), nil
}

func (s *Store) GetBrigade(_ context.Context, id string) (domain.Brigade, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.brigades[id]
	if !ok {
		return domain.Brigade{}, store.ErrNotFound
	}
	return v, nil
}

func (s *Store) UpsertBrigade(_ context.Context, b domain.Brigade) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.brigades[b.ID] = b
	return nil
}

func (s *Store) ListRoads(context.Context) ([]domain.Road, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.roads, func(a, b domain.Road) bool { return a.ID < b.ID }), nil
}

func (s *Store) GetRoad(_ context.Context, id string) (domain.Road, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.roads[id]
	if !ok {
		return domain.Road{}, store.ErrNotFound
	}
	return v, nil
}

func (s *Store) UpsertRoad(_ context.Context, r domain.Road) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roads[r.ID] = r
	return nil
}

func (s *Store) ListAlerts(context.Context) ([]domain.Alert, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.alerts, func(a, b domain.Alert) bool { return a.CreatedAt.After(b.CreatedAt) }), nil
}

func (s *Store) GetAlert(_ context.Context, id string) (domain.Alert, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.alerts[id]
	if !ok {
		return domain.Alert{}, store.ErrNotFound
	}
	return v, nil
}

func (s *Store) UpsertAlert(_ context.Context, a domain.Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alerts[a.ID] = a
	return nil
}
