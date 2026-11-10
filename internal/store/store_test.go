package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/adwitiyashukla/uber-replica/internal/engine"
)

func TestWritesTripsEventsAndLedger(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres test")
	}
	ctx := context.Background()
	boot := time.Now().Format("150405.000")
	s, err := Open(ctx, url, boot, time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC), 50*time.Millisecond, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id := "T-test-" + boot
	trip := &engine.TripView{
		ID: id, RiderID: "R-store", Product: engine.Standard, State: engine.Completed,
		Fare: engine.Fare{Subtotal: 2000, Total: 2400}, DriverPay: 1500, RequestedAt: 86400000, EndedAt: 86500000,
		Driver: &engine.DriverCard{ID: "D1"},
	}
	journal := &engine.Journal{ID: "J-" + boot, TripID: id, At: 86500000, Kind: "fare", Lines: []engine.Line{
		{Account: "rider:R-store", Cents: -2400}, {Account: "driver:D1", Cents: 1500}, {Account: "platform", Cents: 900},
	}}
	batch := []engine.Event{
		{Seq: 1, At: 86400000, Kind: engine.TripRequested, Trip: trip, RiderID: "R-store"},
		{Seq: 2, At: 86500000, Kind: engine.TripCompleted, Trip: trip, DriverID: "D1"},
		{Seq: 3, At: 86500000, Kind: engine.Posted, Journal: journal},
	}
	if err := s.writeTx(ctx, batch); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Trip(ctx, id)
	if err != nil || doc == nil {
		t.Fatalf("trip not stored: %v", err)
	}
	evs, err := s.Events(ctx, id)
	if err != nil || len(evs) != 3 {
		t.Fatalf("expected 3 events, got %d (%v)", len(evs), err)
	}
	hist, err := s.History(ctx, "R-store", 5)
	if err != nil || len(hist) == 0 {
		t.Fatalf("history empty: %v", err)
	}
	var sum int64
	if err := s.db.QueryRowContext(ctx, `select coalesce(sum(cents), 0) from ledger where trip_id = $1`, id).Scan(&sum); err != nil || sum != 0 {
		t.Fatalf("ledger for the trip should sum to zero, got %d (%v)", sum, err)
	}
}
