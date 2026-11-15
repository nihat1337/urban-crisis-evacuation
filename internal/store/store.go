// Package store defines the persistence boundary. Implementations live in
// sub-packages (memory, postgres) and are interchangeable at runtime, which is
// what lets the system switch database without touching business logic.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

var ErrNotFound = errors.New("not found")

type Store interface {
	Name() string
	Ping(ctx context.Context) error
	Close()

	ListZones(ctx context.Context) ([]domain.Zone, error)
	UpsertZone(ctx context.Context, z domain.Zone) error

	ListShelters(ctx context.Context) ([]domain.Shelter, error)
	GetShelter(ctx context.Context, id string) (domain.Shelter, error)
	UpsertShelter(ctx context.Context, s domain.Shelter) error

	CreateSOS(ctx context.Context, s domain.SOS) error
	UpdateSOS(ctx context.Context, s domain.SOS) error
	GetSOS(ctx context.Context, id string) (domain.SOS, error)
	// ListSOS returns signals created at or after since (zero time = all), newest first.
	ListSOS(ctx context.Context, since time.Time) ([]domain.SOS, error)

	UpsertCitizen(ctx context.Context, c domain.Citizen) error
	ListCitizens(ctx context.Context) ([]domain.Citizen, error)

	ListBrigades(ctx context.Context) ([]domain.Brigade, error)
	GetBrigade(ctx context.Context, id string) (domain.Brigade, error)
	UpsertBrigade(ctx context.Context, b domain.Brigade) error

	ListRoads(ctx context.Context) ([]domain.Road, error)
	GetRoad(ctx context.Context, id string) (domain.Road, error)
	UpsertRoad(ctx context.Context, r domain.Road) error

	ListAlerts(ctx context.Context) ([]domain.Alert, error)
	GetAlert(ctx context.Context, id string) (domain.Alert, error)
	UpsertAlert(ctx context.Context, a domain.Alert) error
}
