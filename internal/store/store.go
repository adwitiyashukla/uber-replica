package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/lib/pq"

	"github.com/adwitiyashukla/uber-replica/internal/engine"
)

const schema = `
create table if not exists trips (
	id text primary key,
	rider_id text not null,
	driver_id text,
	simulated boolean not null,
	product text not null,
	state text not null,
	pickup_zone int not null,
	dropoff_zone int not null,
	requested_at timestamptz not null,
	ended_at timestamptz,
	fare_cents int not null,
	driver_pay_cents int not null,
	surge real not null,
	meters real not null,
	cancel_reason text,
	rating smallint,
	doc jsonb not null
);
create index if not exists trips_by_rider on trips (rider_id, requested_at desc);
create index if not exists trips_by_end on trips (ended_at);
create table if not exists trip_events (
	id bigserial primary key,
	boot text not null,
	seq bigint not null,
	at timestamptz not null,
	kind text not null,
	trip_id text,
	driver_id text,
	reason text
);
create index if not exists trip_events_by_trip on trip_events (trip_id, id);
create index if not exists trip_events_by_at on trip_events (at);
create table if not exists ledger (
	id bigserial primary key,
	journal_id text not null,
	trip_id text not null,
	kind text not null,
	account text not null,
	cents bigint not null,
	at timestamptz not null
);
create index if not exists ledger_by_trip on ledger (trip_id);
create index if not exists ledger_by_at on ledger (at);
`

type Store struct {
	db        *sql.DB
	boot      string
	origin    time.Time
	ch        chan engine.Event
	flush     time.Duration
	retention time.Duration
	Dropped   atomic.Int64
	Written   atomic.Int64
}

func Open(ctx context.Context, url, boot string, origin time.Time, flush, retention time.Duration) (*Store, error) {
	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err = db.PingContext(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			db.Close()
			return nil, fmt.Errorf("postgres not reachable: %w", err)
		}
		time.Sleep(time.Second)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{
		db: db, boot: boot, origin: origin, ch: make(chan engine.Event, 20000),
		flush: flush, retention: retention,
	}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) At(ms int64) time.Time { return s.origin.Add(time.Duration(ms) * time.Millisecond) }

func (s *Store) Enqueue(ev engine.Event) {
	switch ev.Kind {
	case engine.SurgeChanged, engine.DriverRouted:
		return
	}
	select {
	case s.ch <- ev:
	default:
		s.Dropped.Add(1)
	}
}

func (s *Store) Run(ctx context.Context) {
	tick := time.NewTicker(s.flush)
	clean := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	defer clean.Stop()
	var batch []engine.Event
	for {
		select {
		case <-ctx.Done():
			s.write(context.Background(), batch)
			return
		case ev := <-s.ch:
			batch = append(batch, ev)
			if len(batch) >= 2000 {
				s.write(ctx, batch)
				batch = batch[:0]
			}
		case <-tick.C:
			s.write(ctx, batch)
			batch = batch[:0]
		case <-clean.C:
			s.prune(ctx)
		}
	}
}

func (s *Store) write(ctx context.Context, batch []engine.Event) {
	if len(batch) == 0 {
		return
	}
	if err := s.writeTx(ctx, batch); err != nil {
		log.Printf("store: dropping %d events: %v", len(batch), err)
		s.Dropped.Add(int64(len(batch)))
		return
	}
	s.Written.Add(int64(len(batch)))
}

func (s *Store) writeTx(ctx context.Context, batch []engine.Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	events, err := tx.PrepareContext(ctx, pq.CopyIn("trip_events", "boot", "seq", "at", "kind", "trip_id", "driver_id", "reason"))
	if err != nil {
		return err
	}
	latest := map[string]*engine.TripView{}
	var order []string
	var journals []*engine.Journal
	for _, ev := range batch {
		var tripID any
		if ev.Trip != nil {
			tripID = ev.Trip.ID
			if _, seen := latest[ev.Trip.ID]; !seen {
				order = append(order, ev.Trip.ID)
			}
			latest[ev.Trip.ID] = ev.Trip
		} else if ev.Offer != nil {
			tripID = ev.Offer.TripID
		} else if ev.Journal != nil {
			tripID = ev.Journal.TripID
			journals = append(journals, ev.Journal)
		}
		var driverID any
		if ev.DriverID != "" {
			driverID = ev.DriverID
		}
		if _, err := events.ExecContext(ctx, s.boot, int64(ev.Seq), s.At(ev.At), ev.Kind.String(), tripID, driverID, ev.Reason); err != nil {
			return err
		}
	}
	if _, err := events.ExecContext(ctx); err != nil {
		return err
	}
	if err := events.Close(); err != nil {
		return err
	}
	upsert, err := tx.PrepareContext(ctx, `
		insert into trips (id, rider_id, driver_id, simulated, product, state, pickup_zone, dropoff_zone,
			requested_at, ended_at, fare_cents, driver_pay_cents, surge, meters, cancel_reason, rating, doc)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		on conflict (id) do update set driver_id = excluded.driver_id, state = excluded.state,
			ended_at = excluded.ended_at, cancel_reason = excluded.cancel_reason, rating = excluded.rating,
			doc = excluded.doc`)
	if err != nil {
		return err
	}
	defer upsert.Close()
	for _, id := range order {
		t := latest[id]
		doc, _ := json.Marshal(t)
		var driverID, ended, reason, rating any
		if t.Driver != nil {
			driverID = t.Driver.ID
		}
		if t.EndedAt > 0 {
			ended = s.At(t.EndedAt)
		}
		if t.CancelReason != "" {
			reason = t.CancelReason
		}
		if t.Rating > 0 {
			rating = t.Rating
		}
		if _, err := upsert.ExecContext(ctx, t.ID, t.RiderID, driverID, t.Simulated, t.Product.String(), t.State.String(),
			int(t.Pickup.Zone), int(t.Dropoff.Zone), s.At(t.RequestedAt), ended, t.Fare.Total, t.DriverPay,
			t.Fare.Surge, t.Meters, reason, rating, string(doc)); err != nil {
			return err
		}
	}
	if len(journals) > 0 {
		ledger, err := tx.PrepareContext(ctx, pq.CopyIn("ledger", "journal_id", "trip_id", "kind", "account", "cents", "at"))
		if err != nil {
			return err
		}
		for _, j := range journals {
			for _, l := range j.Lines {
				if _, err := ledger.ExecContext(ctx, j.ID, j.TripID, j.Kind, l.Account, l.Cents, s.At(j.At)); err != nil {
					return err
				}
			}
		}
		if _, err := ledger.ExecContext(ctx); err != nil {
			return err
		}
		if err := ledger.Close(); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) prune(ctx context.Context) {
	var newest sql.NullTime
	if err := s.db.QueryRowContext(ctx, `select max(at) from trip_events`).Scan(&newest); err != nil || !newest.Valid {
		return
	}
	cut := newest.Time.Add(-s.retention)
	for _, q := range []string{
		`delete from trip_events where at < $1`,
		`delete from ledger where at < $1`,
		`delete from trips where simulated and ended_at < $1`,
	} {
		if _, err := s.db.ExecContext(ctx, q, cut); err != nil {
			log.Printf("store: prune: %v", err)
		}
	}
}

func (s *Store) History(ctx context.Context, riderID string, limit int) ([]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx, `select doc from trips where rider_id = $1 order by requested_at desc limit $2`, riderID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var doc []byte
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(doc))
	}
	return out, rows.Err()
}

func (s *Store) Trip(ctx context.Context, id string) (json.RawMessage, error) {
	var doc []byte
	err := s.db.QueryRowContext(ctx, `select doc from trips where id = $1`, id).Scan(&doc)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return json.RawMessage(doc), err
}

type Totals struct {
	Trips     int64 `json:"trips"`
	Completed int64 `json:"completed"`
	Events    int64 `json:"events"`
	Ledger    int64 `json:"ledgerLines"`
	Balance   int64 `json:"ledgerBalance"`
}

func (s *Store) Totals(ctx context.Context) (Totals, error) {
	var t Totals
	err := s.db.QueryRowContext(ctx, `select
		(select count(*) from trips),
		(select count(*) from trips where state = 'completed'),
		(select count(*) from trip_events),
		(select count(*) from ledger),
		(select coalesce(sum(cents), 0) from ledger)`).Scan(&t.Trips, &t.Completed, &t.Events, &t.Ledger, &t.Balance)
	return t, err
}

type EventRow struct {
	Seq      int64     `json:"seq"`
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	DriverID *string   `json:"driverId"`
	Reason   *string   `json:"reason"`
}

func (s *Store) Events(ctx context.Context, tripID string) ([]EventRow, error) {
	rows, err := s.db.QueryContext(ctx, `select seq, at, kind, driver_id, nullif(reason, '') from trip_events where trip_id = $1 order by id`, tripID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventRow{}
	for rows.Next() {
		var r EventRow
		if err := rows.Scan(&r.Seq, &r.At, &r.Kind, &r.DriverID, &r.Reason); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
