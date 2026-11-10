package sim

import (
	"fmt"
	"strings"

	"github.com/adwitiyashukla/uber-replica/internal/city"
	"github.com/adwitiyashukla/uber-replica/internal/engine"
)

type Metrics struct {
	from, to     int64
	waits        []float64
	matches      []float64
	trips        []float64
	fares        []float64
	surges       []float64
	requested    int
	completed    int
	noDriver     int
	riderCancel  int
	b2b          int
	offers       int
	declines     int
	pickupMeters float64
	tripMeters   float64
	byHour       [24]hourly
}

type hourly struct {
	requested int
	waits     []float64
	fares     []float64
}

func NewMetrics(from, to int64) *Metrics { return &Metrics{from: from, to: to} }

func hourOfDay(ms int64) int { return int((ms / 1000 % 86400) / 3600) }

func (m *Metrics) in(t *engine.TripView) bool {
	return t != nil && t.RequestedAt >= m.from && t.RequestedAt < m.to
}

func (m *Metrics) Observe(ev engine.Event) {
	switch ev.Kind {
	case engine.TripRequested:
		if m.in(ev.Trip) {
			m.requested++
			m.surges = append(m.surges, ev.Trip.Fare.Surge)
			m.byHour[hourOfDay(ev.Trip.RequestedAt)].requested++
		}
	case engine.OfferSent:
		if m.in(ev.Trip) {
			m.offers++
		}
	case engine.OfferClosed:
		if ev.Reason == "declined" || ev.Reason == "expired" {
			m.declines++
		}
	case engine.TripCompleted:
		t := ev.Trip
		if !m.in(t) {
			return
		}
		m.completed++
		m.waits = append(m.waits, float64(t.ArrivedAt-t.RequestedAt)/1000)
		m.matches = append(m.matches, float64(t.AcceptedAt-t.RequestedAt)/1000)
		m.trips = append(m.trips, float64(t.EndedAt-t.StartedAt)/1000)
		m.fares = append(m.fares, float64(t.Fare.Subtotal)/100)
		h := &m.byHour[hourOfDay(t.RequestedAt)]
		h.waits = append(h.waits, float64(t.ArrivedAt-t.RequestedAt)/1000)
		h.fares = append(h.fares, float64(t.Fare.Subtotal)/100)
		if t.B2B {
			m.b2b++
		}
	case engine.TripCancelled:
		if !m.in(ev.Trip) {
			return
		}
		switch ev.Reason {
		case "no_drivers":
			m.noDriver++
		case "rider":
			m.riderCancel++
		}
	case engine.DriverRouted:
		if ev.Route.Purpose == "pickup" {
			m.pickupMeters += ev.Route.Path.Meters
		} else {
			m.tripMeters += ev.Route.Path.Meters
		}
	}
}

type Summary struct {
	Policy      string  `json:"policy"`
	BackToBack  bool    `json:"backToBack"`
	Window      string  `json:"window"`
	Sessions    int     `json:"sessions"`
	Lost        int     `json:"lostToPrice"`
	Requested   int     `json:"requested"`
	Completed   int     `json:"completed"`
	NoDriver    int     `json:"noDriver"`
	RiderCancel int     `json:"riderCancel"`
	Fulfilled   float64 `json:"fulfilled"`
	WaitMean    float64 `json:"waitMean"`
	WaitP50     float64 `json:"waitP50"`
	WaitP90     float64 `json:"waitP90"`
	MatchP50    float64 `json:"matchP50"`
	TripP50     float64 `json:"tripP50"`
	TripP90     float64 `json:"tripP90"`
	FareP50     float64 `json:"fareP50"`
	SurgeMean   float64 `json:"surgeMean"`
	B2BShare    float64 `json:"b2bShare"`
	Deadhead    float64 `json:"deadheadShare"`
	Utilization float64 `json:"utilization"`
	Offers      int     `json:"offers"`
	Declines    int     `json:"declines"`
	PeakFleet   int     `json:"peakFleet"`
	RealWaitP50 float64 `json:"realWaitP50"`
	RealWaitP90 float64 `json:"realWaitP90"`
	RealTripP50 float64 `json:"realTripP50"`
	RealTripP90 float64 `json:"realTripP90"`
	RealFareP50 float64 `json:"realFareP50"`
	RealTrips   int     `json:"realTrips"`
	Hours       []Hour  `json:"hours"`
}

type Hour struct {
	Hour        int     `json:"hour"`
	Requested   int     `json:"requested"`
	Completed   int     `json:"completed"`
	WaitP50     float64 `json:"waitP50"`
	FareP50     float64 `json:"fareP50"`
	RealTrips   int     `json:"realTrips"`
	RealWaitP50 float64 `json:"realWaitP50"`
	RealFareP50 float64 `json:"realFareP50"`
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func (m *Metrics) Summary(c *city.City, s *Sim, utilization float64, peakFleet int) Summary {
	out := Summary{
		Sessions: s.Sessions, Lost: s.Lost, Requested: m.requested, Completed: m.completed,
		NoDriver: m.noDriver, RiderCancel: m.riderCancel, WaitMean: mean(m.waits),
		WaitP50: engine.Percentile(m.waits, 50), WaitP90: engine.Percentile(m.waits, 90),
		MatchP50: engine.Percentile(m.matches, 50), TripP50: engine.Percentile(m.trips, 50),
		TripP90: engine.Percentile(m.trips, 90), FareP50: engine.Percentile(m.fares, 50),
		SurgeMean: mean(m.surges), Utilization: utilization, Offers: m.offers, Declines: m.declines,
		PeakFleet: peakFleet,
	}
	if m.requested > 0 {
		out.Fulfilled = float64(m.completed) / float64(m.requested)
	}
	if m.completed > 0 {
		out.B2BShare = float64(m.b2b) / float64(m.completed)
	}
	if total := m.pickupMeters + m.tripMeters; total > 0 {
		out.Deadhead = m.pickupMeters / total
	}
	var waits, trips, fares []float64
	fromS, toS := float64(m.from%86400000)/1000, float64(m.to%86400000)/1000
	if m.to-m.from >= 86400000 {
		fromS, toS = 0, 86400
	}
	for _, r := range c.Demand {
		t := float64(r.T)
		if t < fromS || t >= toS {
			continue
		}
		waits = append(waits, float64(r.Wait))
		trips = append(trips, float64(r.Trip))
		fares = append(fares, float64(r.Fare))
	}
	out.RealTrips = len(waits)
	out.RealWaitP50, out.RealWaitP90 = engine.Percentile(waits, 50), engine.Percentile(waits, 90)
	out.RealTripP50, out.RealTripP90 = engine.Percentile(trips, 50), engine.Percentile(trips, 90)
	out.RealFareP50 = engine.Percentile(fares, 50)
	for h, b := range m.byHour {
		if b.requested == 0 {
			continue
		}
		real := c.Cal.Real.Hours[h]
		out.Hours = append(out.Hours, Hour{
			Hour: h, Requested: b.requested, Completed: len(b.waits),
			WaitP50: engine.Percentile(b.waits, 50), FareP50: engine.Percentile(b.fares, 50),
			RealTrips: real.Requests, RealWaitP50: real.WaitP50, RealFareP50: real.FareP50,
		})
	}
	return out
}

func clock(ms int64) string {
	s := (ms / 1000) % 86400
	return fmt.Sprintf("%02d:%02d", s/3600, (s%3600)/60)
}

func Window(from, to int64) string {
	if to-from >= 86400000 {
		return "full day"
	}
	return clock(from) + "-" + clock(to)
}

func HourTable(r Summary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-6s %8s %8s %8s %8s %8s %8s\n", "hour", "requests", "real", "wait50", "real", "fare50", "real")
	for _, h := range r.Hours {
		fmt.Fprintf(&b, "%02d:00  %8d %8d %7.0fs %7.0fs %7.2f$ %7.2f$\n", h.Hour, h.Requested, h.RealTrips, h.WaitP50, h.RealWaitP50, h.FareP50, h.RealFareP50)
	}
	return b.String()
}

func Table(rows []Summary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-14s %8s %8s %7s %7s %7s %7s %7s %8s %6s %6s %6s %6s\n", "run", "requests", "done", "fill", "wait50", "wait90", "match50", "trip50", "fare50", "surge", "b2b", "lost", "fleet")
	for _, r := range rows {
		name := r.Policy
		if r.BackToBack {
			name += "+b2b"
		}
		fmt.Fprintf(&b, "%-14s %8d %8d %6.1f%% %6.0fs %6.0fs %6.0fs %6.0fs %7.2f$ %6.2f %5.1f%% %6d %6d\n",
			name, r.Requested, r.Completed, r.Fulfilled*100, r.WaitP50, r.WaitP90, r.MatchP50, r.TripP50, r.FareP50, r.SurgeMean, r.B2BShare*100, r.Lost, r.PeakFleet)
	}
	if len(rows) > 0 {
		r := rows[0]
		fmt.Fprintf(&b, "%-14s %8s %8d %7s %6.0fs %6.0fs %7s %6.0fs %7.2f$\n", "real uber", "", r.RealTrips, "", r.RealWaitP50, r.RealWaitP90, "", r.RealTripP50, r.RealFareP50)
	}
	return b.String()
}
