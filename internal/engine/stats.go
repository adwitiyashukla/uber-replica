package engine

import (
	"math"
	"sort"
)

const ringMinutes = 240

type minute struct {
	start       int64
	used        bool
	requests    int
	completed   int
	cancelled   int
	noDriver    int
	riderCancel int
	waits       []float64
	matches     []float64
	online      int
	busy        int
	surgeSum    float64
	revenue     int64
	pay         int64
}

type Stats struct {
	Requests    int64 `json:"requests"`
	Accepted    int64 `json:"accepted"`
	Completed   int64 `json:"completed"`
	Cancelled   int64 `json:"cancelled"`
	NoDriver    int64 `json:"noDriver"`
	RiderCancel int64 `json:"riderCancel"`
	Offers      int64 `json:"offers"`
	Declined    int64 `json:"declined"`
	Expired     int64 `json:"expired"`
	ring        [ringMinutes]minute
}

func newStats() *Stats { return &Stats{} }

func (s *Stats) bucket(now int64) *minute {
	m := now / 60000
	b := &s.ring[((m%ringMinutes)+ringMinutes)%ringMinutes]
	if !b.used || b.start != m*60000 {
		*b = minute{start: m * 60000, used: true}
	}
	return b
}

func (s *Stats) request(e *Engine, t *Trip) {
	s.Requests++
	b := s.bucket(e.now)
	b.requests++
	b.surgeSum += t.Fare.Surge
}

func (s *Stats) accepted(e *Engine, t *Trip) {
	s.Accepted++
	b := s.bucket(e.now)
	b.matches = append(b.matches, float64(e.now-t.RequestedAt)/1000)
}

func (s *Stats) arrived(e *Engine, t *Trip) {
	b := s.bucket(e.now)
	b.waits = append(b.waits, float64(t.ArrivedAt-t.RequestedAt)/1000)
}

func (s *Stats) completed(e *Engine, t *Trip) {
	s.Completed++
	b := s.bucket(e.now)
	b.completed++
	b.revenue += t.Fare.Subtotal
	b.pay += t.DriverPay
}

func (s *Stats) cancelled(e *Engine, t *Trip) {
	s.Cancelled++
	b := s.bucket(e.now)
	b.cancelled++
	switch t.CancelReason {
	case "no_drivers":
		s.NoDriver++
		b.noDriver++
	case "rider":
		s.RiderCancel++
		b.riderCancel++
	}
}

func (s *Stats) closeMinute(e *Engine) {
	b := s.bucket(e.now - 1)
	b.online = e.Online()
	b.busy = e.counts[EnRoute] + e.counts[AtPickup] + e.counts[OnTrip]
}

func Percentile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	pos := q / 100 * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return s[lo] + (s[hi]-s[lo])*(pos-float64(lo))
}

type SeriesPoint struct {
	At        int64   `json:"at"`
	Requests  int     `json:"requests"`
	Completed int     `json:"completed"`
	WaitP50   float64 `json:"waitP50"`
	Online    int     `json:"online"`
	Busy      int     `json:"busy"`
	Surge     float64 `json:"surge"`
}

type StatsView struct {
	At          int64         `json:"at"`
	Online      int           `json:"online"`
	Idle        int           `json:"idle"`
	Offered     int           `json:"offered"`
	Busy        int           `json:"busy"`
	Open        int           `json:"open"`
	Requests60  int           `json:"requests60"`
	Completed60 int           `json:"completed60"`
	Cancelled60 int           `json:"cancelled60"`
	NoDriver60  int           `json:"noDriver60"`
	WaitP50     float64       `json:"waitP50"`
	WaitP90     float64       `json:"waitP90"`
	MatchP50    float64       `json:"matchP50"`
	Fulfilled   float64       `json:"fulfilled"`
	Utilization float64       `json:"utilization"`
	SurgeCells  int           `json:"surgeCells"`
	SurgeMax    float64       `json:"surgeMax"`
	SurgeMean   float64       `json:"surgeMean"`
	Revenue60   int64         `json:"revenue60"`
	DriverPay60 int64         `json:"driverPay60"`
	Policy      string        `json:"policy"`
	BackToBack  bool          `json:"backToBack"`
	Totals      Stats         `json:"totals"`
	Series      []SeriesPoint `json:"series"`
}

func (e *Engine) Online() int {
	return e.Count(Idle) + e.Count(Offered) + e.Count(EnRoute) + e.Count(AtPickup) + e.Count(OnTrip)
}

func (e *Engine) StatsView() StatsView {
	s := e.stats
	v := StatsView{
		At: e.now, Online: e.Online(), Idle: e.counts[Idle], Offered: e.counts[Offered],
		Busy: e.counts[EnRoute] + e.counts[AtPickup] + e.counts[OnTrip], Open: len(e.open),
		Totals: *s,
	}
	v.Totals.ring = [ringMinutes]minute{}
	v.Policy, v.BackToBack = e.Policy()
	v.SurgeCells, v.SurgeMax, v.SurgeMean = e.surge.summary()
	if v.Online > 0 {
		v.Utilization = float64(v.Busy) / float64(v.Online)
	}
	cur := e.now / 60000
	var waits30, matches30 []float64
	for k := int64(60); k >= 1; k-- {
		m := cur - k
		b := &s.ring[((m%ringMinutes)+ringMinutes)%ringMinutes]
		if !b.used || b.start != m*60000 {
			continue
		}
		v.Requests60 += b.requests
		v.Completed60 += b.completed
		v.Cancelled60 += b.cancelled
		v.NoDriver60 += b.noDriver
		v.Revenue60 += b.revenue
		v.DriverPay60 += b.pay
		if k <= 30 {
			waits30 = append(waits30, b.waits...)
			matches30 = append(matches30, b.matches...)
		}
		p := SeriesPoint{At: b.start, Requests: b.requests, Completed: b.completed, WaitP50: Percentile(b.waits, 50), Online: b.online, Busy: b.busy}
		if b.requests > 0 {
			p.Surge = b.surgeSum / float64(b.requests)
		}
		v.Series = append(v.Series, p)
	}
	v.WaitP50, v.WaitP90 = Percentile(waits30, 50), Percentile(waits30, 90)
	v.MatchP50 = Percentile(matches30, 50)
	if done := v.Completed60 + v.Cancelled60; done > 0 {
		v.Fulfilled = float64(v.Completed60) / float64(done)
	}
	return v
}

func (e *Engine) Utilization() float64 {
	busy, on := e.retired[0], e.retired[1]
	for _, d := range e.byNum {
		b, o := d.BusyMs, d.OnlineMs
		if d.State != Offline {
			o += e.now - d.OnlineAt
		}
		if d.State.Busy() {
			b += e.now - d.busyFrom
		}
		busy += b
		on += o
	}
	if on == 0 {
		return 0
	}
	return float64(busy) / float64(on)
}
