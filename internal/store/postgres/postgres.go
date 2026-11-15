// Package postgres is the production Store backed by PostgreSQL (pgx).
// Schema migration is idempotent and runs on start-up.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
	"github.com/nihat1337/urban-crisis-evacuation/internal/store"
)

type Store struct {
	pool *pgxpool.Pool
}

var _ store.Store = (*Store)(nil)

// New connects (retrying while the database container boots) and migrates.
func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt < 15; attempt++ {
		if lastErr = pool.Ping(ctx); lastErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if lastErr != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres unreachable: %w", lastErr)
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Name() string               { return "postgres" }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *Store) Close()                     { s.pool.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS zones (
	id text PRIMARY KEY, name text NOT NULL,
	lat double precision NOT NULL, lng double precision NOT NULL, radius_m double precision NOT NULL
);
CREATE TABLE IF NOT EXISTS shelters (
	id text PRIMARY KEY, name text NOT NULL, type text NOT NULL, zone_id text NOT NULL,
	lat double precision NOT NULL, lng double precision NOT NULL,
	capacity int NOT NULL, occupancy int NOT NULL, active boolean NOT NULL
);
CREATE TABLE IF NOT EXISTS sos (
	id text PRIMARY KEY, citizen_id text NOT NULL, type text NOT NULL, zone_id text NOT NULL,
	lat double precision NOT NULL, lng double precision NOT NULL, message text NOT NULL DEFAULT '',
	status text NOT NULL, brigade_id text NOT NULL DEFAULT '', created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS sos_created_idx ON sos (created_at DESC);
CREATE TABLE IF NOT EXISTS citizens (
	id text PRIMARY KEY, status text NOT NULL,
	lat double precision, lng double precision, updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS brigades (
	id text PRIMARY KEY, name text NOT NULL, status text NOT NULL,
	lat double precision NOT NULL, lng double precision NOT NULL, zone_id text NOT NULL,
	assigned_sos_id text NOT NULL DEFAULT '', updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS roads (
	id text PRIMARY KEY, name text NOT NULL,
	from_lat double precision NOT NULL, from_lng double precision NOT NULL,
	to_lat double precision NOT NULL, to_lng double precision NOT NULL,
	status text NOT NULL, updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS alerts (
	id text PRIMARY KEY, level text NOT NULL, kind text NOT NULL, zone_id text NOT NULL,
	shelter_id text NOT NULL DEFAULT '', lat double precision NOT NULL, lng double precision NOT NULL,
	message text NOT NULL, signal_count int NOT NULL DEFAULT 0, suggested_actions text[] NOT NULL,
	executed boolean NOT NULL, executed_action text NOT NULL DEFAULT '',
	created_at timestamptz NOT NULL, executed_at timestamptz
);
-- Priority SOS / accessibility module (announced at the final): idempotent
-- upgrades so an existing database keeps working after the change.
ALTER TABLE shelters ADD COLUMN IF NOT EXISTS accessible boolean NOT NULL DEFAULT true;
ALTER TABLE roads    ADD COLUMN IF NOT EXISTS accessible boolean NOT NULL DEFAULT true;
ALTER TABLE citizens ADD COLUMN IF NOT EXISTS role text NOT NULL DEFAULT 'standard';
ALTER TABLE sos      ADD COLUMN IF NOT EXISTS role text NOT NULL DEFAULT 'standard';
ALTER TABLE sos      ADD COLUMN IF NOT EXISTS priority int NOT NULL DEFAULT 2;
CREATE INDEX IF NOT EXISTS sos_priority_idx ON sos (priority, created_at DESC);
-- Citizen profile + SMS notifications module.
ALTER TABLE citizens ADD COLUMN IF NOT EXISTS name  text NOT NULL DEFAULT '';
ALTER TABLE citizens ADD COLUMN IF NOT EXISTS phone text NOT NULL DEFAULT '';
ALTER TABLE citizens ADD COLUMN IF NOT EXISTS email text NOT NULL DEFAULT '';
ALTER TABLE sos ADD COLUMN IF NOT EXISTS citizen_name  text NOT NULL DEFAULT '';
ALTER TABLE sos ADD COLUMN IF NOT EXISTS citizen_phone text NOT NULL DEFAULT '';
ALTER TABLE sos ADD COLUMN IF NOT EXISTS citizen_email text NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS notifications (
	id text PRIMARY KEY, citizen_id text NOT NULL, sos_id text NOT NULL DEFAULT '',
	channel text NOT NULL, to_addr text NOT NULL, message text NOT NULL, reason text NOT NULL,
	provider text NOT NULL, provider_id text NOT NULL DEFAULT '', status text NOT NULL,
	error text NOT NULL DEFAULT '', created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS notifications_created_idx ON notifications (created_at DESC);
`

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

func wrapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}

// --- zones ---

func (s *Store) ListZones(ctx context.Context) ([]domain.Zone, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, lat, lng, radius_m FROM zones ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Zone
	for rows.Next() {
		var z domain.Zone
		if err := rows.Scan(&z.ID, &z.Name, &z.Center.Lat, &z.Center.Lng, &z.RadiusM); err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

func (s *Store) UpsertZone(ctx context.Context, z domain.Zone) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO zones (id, name, lat, lng, radius_m) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, lat=EXCLUDED.lat, lng=EXCLUDED.lng, radius_m=EXCLUDED.radius_m`,
		z.ID, z.Name, z.Center.Lat, z.Center.Lng, z.RadiusM)
	return err
}

// --- shelters ---

const shelterCols = `id, name, type, zone_id, lat, lng, capacity, occupancy, active, accessible`

func scanShelter(row pgx.Row) (domain.Shelter, error) {
	var sh domain.Shelter
	err := row.Scan(&sh.ID, &sh.Name, &sh.Type, &sh.ZoneID, &sh.Location.Lat, &sh.Location.Lng, &sh.Capacity, &sh.Occupancy, &sh.Active, &sh.Accessible)
	return sh, err
}

func (s *Store) ListShelters(ctx context.Context) ([]domain.Shelter, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+shelterCols+` FROM shelters ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Shelter
	for rows.Next() {
		sh, err := scanShelter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

func (s *Store) GetShelter(ctx context.Context, id string) (domain.Shelter, error) {
	sh, err := scanShelter(s.pool.QueryRow(ctx, `SELECT `+shelterCols+` FROM shelters WHERE id=$1`, id))
	return sh, wrapNotFound(err)
}

func (s *Store) UpsertShelter(ctx context.Context, sh domain.Shelter) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO shelters (`+shelterCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, type=EXCLUDED.type, zone_id=EXCLUDED.zone_id,
		lat=EXCLUDED.lat, lng=EXCLUDED.lng, capacity=EXCLUDED.capacity, occupancy=EXCLUDED.occupancy, active=EXCLUDED.active,
		accessible=EXCLUDED.accessible`,
		sh.ID, sh.Name, sh.Type, sh.ZoneID, sh.Location.Lat, sh.Location.Lng, sh.Capacity, sh.Occupancy, sh.Active, sh.Accessible)
	return err
}

// --- sos ---

const sosCols = `id, citizen_id, type, zone_id, lat, lng, message, status, brigade_id, created_at, role, priority, citizen_name, citizen_phone, citizen_email`

func scanSOS(row pgx.Row) (domain.SOS, error) {
	var v domain.SOS
	err := row.Scan(&v.ID, &v.CitizenID, &v.Type, &v.ZoneID, &v.Location.Lat, &v.Location.Lng, &v.Message, &v.Status, &v.BrigadeID, &v.CreatedAt, &v.Role, &v.Priority,
		&v.CitizenName, &v.CitizenPhone, &v.CitizenEmail)
	return v, err
}

func (s *Store) CreateSOS(ctx context.Context, v domain.SOS) error {
	if v.Role == "" {
		v.Role = domain.RoleStandard
	}
	if v.Priority == 0 {
		v.Priority = domain.PriorityNormal
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO sos (`+sosCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		v.ID, v.CitizenID, v.Type, v.ZoneID, v.Location.Lat, v.Location.Lng, v.Message, v.Status, v.BrigadeID, v.CreatedAt, v.Role, v.Priority,
		v.CitizenName, v.CitizenPhone, v.CitizenEmail)
	return err
}

func (s *Store) UpdateSOS(ctx context.Context, v domain.SOS) error {
	tag, err := s.pool.Exec(ctx, `UPDATE sos SET status=$2, brigade_id=$3, message=$4 WHERE id=$1`, v.ID, v.Status, v.BrigadeID, v.Message)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) GetSOS(ctx context.Context, id string) (domain.SOS, error) {
	v, err := scanSOS(s.pool.QueryRow(ctx, `SELECT `+sosCols+` FROM sos WHERE id=$1`, id))
	return v, wrapNotFound(err)
}

func (s *Store) ListSOS(ctx context.Context, since time.Time) ([]domain.SOS, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+sosCols+` FROM sos WHERE created_at >= $1 ORDER BY priority ASC, created_at DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SOS
	for rows.Next() {
		v, err := scanSOS(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// --- citizens ---

func (s *Store) UpsertCitizen(ctx context.Context, c domain.Citizen) error {
	var lat, lng *float64
	if c.Location != nil {
		lat, lng = &c.Location.Lat, &c.Location.Lng
	}
	if c.Role == "" {
		c.Role = domain.RoleStandard
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO citizens (id, status, lat, lng, updated_at, role, name, phone, email) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (id) DO UPDATE SET status=EXCLUDED.status, lat=EXCLUDED.lat, lng=EXCLUDED.lng, updated_at=EXCLUDED.updated_at,
		role=EXCLUDED.role, name=EXCLUDED.name, phone=EXCLUDED.phone, email=EXCLUDED.email`,
		c.ID, c.Status, lat, lng, c.UpdatedAt, c.Role, c.Name, c.Phone, c.Email)
	return err
}

const citizenCols = `id, status, lat, lng, updated_at, role, name, phone, email`

func scanCitizen(row pgx.Row) (domain.Citizen, error) {
	var c domain.Citizen
	var lat, lng *float64
	err := row.Scan(&c.ID, &c.Status, &lat, &lng, &c.UpdatedAt, &c.Role, &c.Name, &c.Phone, &c.Email)
	if lat != nil && lng != nil {
		c.Location = &domain.Point{Lat: *lat, Lng: *lng}
	}
	return c, err
}

func (s *Store) GetCitizen(ctx context.Context, id string) (domain.Citizen, error) {
	c, err := scanCitizen(s.pool.QueryRow(ctx, `SELECT `+citizenCols+` FROM citizens WHERE id=$1`, id))
	return c, wrapNotFound(err)
}

func (s *Store) ListCitizens(ctx context.Context) ([]domain.Citizen, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+citizenCols+` FROM citizens ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Citizen
	for rows.Next() {
		c, err := scanCitizen(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- notifications ---

const notifCols = `id, citizen_id, sos_id, channel, to_addr, message, reason, provider, provider_id, status, error, created_at`

func (s *Store) CreateNotification(ctx context.Context, n domain.Notification) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO notifications (`+notifCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		n.ID, n.CitizenID, n.SOSID, n.Channel, n.To, n.Message, n.Reason, n.Provider, n.ProviderID, n.Status, n.Error, n.CreatedAt)
	return err
}

func (s *Store) ListNotifications(ctx context.Context, limit int) ([]domain.Notification, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT `+notifCols+` FROM notifications ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Notification
	for rows.Next() {
		var n domain.Notification
		if err := rows.Scan(&n.ID, &n.CitizenID, &n.SOSID, &n.Channel, &n.To, &n.Message, &n.Reason, &n.Provider, &n.ProviderID, &n.Status, &n.Error, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// --- brigades ---

const brigadeCols = `id, name, status, lat, lng, zone_id, assigned_sos_id, updated_at`

func scanBrigade(row pgx.Row) (domain.Brigade, error) {
	var b domain.Brigade
	err := row.Scan(&b.ID, &b.Name, &b.Status, &b.Location.Lat, &b.Location.Lng, &b.ZoneID, &b.AssignedSOSID, &b.UpdatedAt)
	return b, err
}

func (s *Store) ListBrigades(ctx context.Context) ([]domain.Brigade, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+brigadeCols+` FROM brigades ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Brigade
	for rows.Next() {
		b, err := scanBrigade(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) GetBrigade(ctx context.Context, id string) (domain.Brigade, error) {
	b, err := scanBrigade(s.pool.QueryRow(ctx, `SELECT `+brigadeCols+` FROM brigades WHERE id=$1`, id))
	return b, wrapNotFound(err)
}

func (s *Store) UpsertBrigade(ctx context.Context, b domain.Brigade) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO brigades (`+brigadeCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, status=EXCLUDED.status, lat=EXCLUDED.lat, lng=EXCLUDED.lng,
		zone_id=EXCLUDED.zone_id, assigned_sos_id=EXCLUDED.assigned_sos_id, updated_at=EXCLUDED.updated_at`,
		b.ID, b.Name, b.Status, b.Location.Lat, b.Location.Lng, b.ZoneID, b.AssignedSOSID, b.UpdatedAt)
	return err
}

// --- roads ---

const roadCols = `id, name, from_lat, from_lng, to_lat, to_lng, status, updated_at, accessible`

func scanRoad(row pgx.Row) (domain.Road, error) {
	var r domain.Road
	err := row.Scan(&r.ID, &r.Name, &r.From.Lat, &r.From.Lng, &r.To.Lat, &r.To.Lng, &r.Status, &r.UpdatedAt, &r.Accessible)
	return r, err
}

func (s *Store) ListRoads(ctx context.Context) ([]domain.Road, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+roadCols+` FROM roads ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Road
	for rows.Next() {
		r, err := scanRoad(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRoad(ctx context.Context, id string) (domain.Road, error) {
	r, err := scanRoad(s.pool.QueryRow(ctx, `SELECT `+roadCols+` FROM roads WHERE id=$1`, id))
	return r, wrapNotFound(err)
}

func (s *Store) UpsertRoad(ctx context.Context, r domain.Road) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO roads (`+roadCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, from_lat=EXCLUDED.from_lat, from_lng=EXCLUDED.from_lng,
		to_lat=EXCLUDED.to_lat, to_lng=EXCLUDED.to_lng, status=EXCLUDED.status, updated_at=EXCLUDED.updated_at,
		accessible=EXCLUDED.accessible`,
		r.ID, r.Name, r.From.Lat, r.From.Lng, r.To.Lat, r.To.Lng, r.Status, r.UpdatedAt, r.Accessible)
	return err
}

// --- alerts ---

const alertCols = `id, level, kind, zone_id, shelter_id, lat, lng, message, signal_count, suggested_actions, executed, executed_action, created_at, executed_at`

func scanAlert(row pgx.Row) (domain.Alert, error) {
	var a domain.Alert
	var actions []string
	err := row.Scan(&a.ID, &a.Level, &a.Kind, &a.ZoneID, &a.ShelterID, &a.Center.Lat, &a.Center.Lng, &a.Message,
		&a.SignalCount, &actions, &a.Executed, &a.ExecutedAction, &a.CreatedAt, &a.ExecutedAt)
	for _, s := range actions {
		a.SuggestedActions = append(a.SuggestedActions, domain.AlertAction(s))
	}
	return a, err
}

func (s *Store) ListAlerts(ctx context.Context) ([]domain.Alert, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+alertCols+` FROM alerts ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAlert(ctx context.Context, id string) (domain.Alert, error) {
	a, err := scanAlert(s.pool.QueryRow(ctx, `SELECT `+alertCols+` FROM alerts WHERE id=$1`, id))
	return a, wrapNotFound(err)
}

func (s *Store) UpsertAlert(ctx context.Context, a domain.Alert) error {
	actions := make([]string, 0, len(a.SuggestedActions))
	for _, x := range a.SuggestedActions {
		actions = append(actions, string(x))
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO alerts (`+alertCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (id) DO UPDATE SET executed=EXCLUDED.executed, executed_action=EXCLUDED.executed_action, executed_at=EXCLUDED.executed_at`,
		a.ID, a.Level, a.Kind, a.ZoneID, a.ShelterID, a.Center.Lat, a.Center.Lng, a.Message, a.SignalCount, actions,
		a.Executed, a.ExecutedAction, a.CreatedAt, a.ExecutedAt)
	return err
}
