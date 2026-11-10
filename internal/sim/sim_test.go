package sim

import (
	"sync"
	"testing"

	"github.com/adwitiyashukla/uber-replica/internal/city"
	"github.com/adwitiyashukla/uber-replica/internal/config"
	"github.com/adwitiyashukla/uber-replica/internal/engine"
)

var (
	once    sync.Once
	loaded  *city.City
	loadErr error
)

func manhattan(t testing.TB) *city.City {
	once.Do(func() { loaded, loadErr = city.Load("../../data") })
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	return loaded
}

const (
	day     = int64(86400000)
	eightAM = day + 8*3600*1000
)

func run(t *testing.T, minutes int, edit func(*config.Config)) (*engine.Engine, *Sim, []engine.Event, *Metrics) {
	cfg := config.Default()
	cfg.Sim.DemandScale = 0.25
	cfg.Sim.LocationEveryS = 4
	if edit != nil {
		edit(&cfg)
	}
	c := manhattan(t)
	e := engine.New(cfg, c, []byte("sim-test"))
	begin := eightAM - 20*60000
	e.Start(begin)
	s := New(cfg, c, e)
	m := NewMetrics(eightAM, eightAM+int64(minutes)*60000)
	var all []engine.Event
	feed := func() {
		for _, ev := range e.Drain() {
			all = append(all, ev)
			s.Observe(ev)
			m.Observe(ev)
		}
	}
	s.Start(begin)
	feed()
	for now := begin; now <= eightAM+int64(minutes)*60000; now += 1000 {
		e.Advance(now)
		feed()
		s.Step(now)
		feed()
	}
	return e, s, all, m
}

func TestReplayServesRealDemand(t *testing.T) {
	e, s, _, m := run(t, 40, nil)
	if m.requested < 300 {
		t.Fatalf("only %d requests in 40 minutes of the 8 AM rush", m.requested)
	}
	if m.completed*5 < m.requested*2 {
		t.Fatalf("too few trips finished inside the window: %d of %d", m.completed, m.requested)
	}
	sum := m.Summary(manhattan(t), s, e.Utilization(), s.OnlineDrivers())
	if sum.WaitP50 <= 0 || sum.WaitP50 > 900 || sum.TripP50 <= 0 {
		t.Fatalf("implausible summary %+v", sum)
	}
	var total int64
	for _, v := range e.Books() {
		total += v
	}
	if total != 0 {
		t.Fatalf("ledger does not balance: %v", e.Books())
	}
}

func TestReplayIsDeterministic(t *testing.T) {
	_, _, a, _ := run(t, 15, nil)
	_, _, b, _ := run(t, 15, nil)
	if engine.Fingerprint(a) != engine.Fingerprint(b) || len(a) != len(b) {
		t.Fatalf("two runs with the same seed differ: %d vs %d events", len(a), len(b))
	}
	_, _, c, _ := run(t, 15, func(c *config.Config) { c.Sim.Seed = 99 })
	if engine.Fingerprint(a) == engine.Fingerprint(c) {
		t.Fatal("a different seed should change the run")
	}
}

func TestFleetFollowsTarget(t *testing.T) {
	_, s, _, _ := run(t, 5, nil)
	target := s.Target(eightAM + 5*60000)
	got := s.OnlineDrivers()
	if got < target*8/10 || got > target*12/10 {
		t.Fatalf("fleet %d is far from the target %d", got, target)
	}
}

func TestWillingnessUsesWhatTheRiderPaid(t *testing.T) {
	c := manhattan(t)
	s := New(config.Default(), c, nil)
	f := c.Cal.Fare
	base := city.Request{Miles: 2, Trip: 900}
	model := f.Base + f.PerMile*2 + f.PerMinute*15
	base.Fare = float32(model * 1.5)
	if w := s.willingness(base); w < 1.45 || w > 1.55 {
		t.Fatalf("rider who paid 1.5x should tolerate about 1.5x, got %v", w)
	}
	base.Fare = float32(model * 0.5)
	if w := s.willingness(base); w != 1 {
		t.Fatalf("discounted trips still accept normal prices, got %v", w)
	}
}

func TestRecentRequestRate(t *testing.T) {
	s := &Sim{}
	base := int64(86400000)
	for m := int64(0); m < 20; m++ {
		for i := 0; i < 3; i++ {
			s.noteRequest(base + m*60000 + 1000)
		}
	}
	if r := s.recentRate(base + 20*60000); r != 3 {
		t.Fatalf("a steady 3 requests a minute should read 3, got %v", r)
	}
	if r := s.recentRate(base + 30*60000); r != 1 {
		t.Fatalf("ten quiet minutes should pull the rate down to 1, got %v", r)
	}
	if r := s.recentRate(base + 60*60000); r != 0 {
		t.Fatalf("an hour later the rate should be 0, got %v", r)
	}
}
