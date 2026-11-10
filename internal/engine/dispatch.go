package engine

import (
	"math"
	"slices"
	"sort"

	"github.com/adwitiyashukla/uber-replica/internal/geo"
)

const (
	unmatched    = 1e9
	visitorReach = 3000
)

type candidate struct {
	d   *Driver
	eta float64
	b2b bool
}

func (e *Engine) candidates(t *Trip) []candidate {
	var pool []candidate
	rings := geo.Rings(geo.Cell(t.Pickup.Pt, indexRes), e.cfgD.SearchRings)
	for k, ring := range rings {
		for _, c := range ring {
			for _, d := range e.idle[c] {
				if d.Product.Serves(t.Product) && !t.declinedBy(d.ID) {
					pool = append(pool, candidate{d: d})
				}
			}
			if !e.backToBack {
				continue
			}
			for _, d := range e.b2b[c] {
				if d.Product.Serves(t.Product) && !t.declinedBy(d.ID) && d.Next == nil && d.Offer == nil &&
					e.remaining(d) <= float64(e.cfgD.B2BMaxRemainingS) {
					pool = append(pool, candidate{d: d, b2b: true})
				}
			}
		}
		if len(pool) >= e.cfgD.MaxCandidates && k >= 1 {
			break
		}
	}
	if e.cfgD.VisitorHeadStartS > 0 {
		for _, d := range e.humans {
			if d.inIdle && d.Product.Serves(t.Product) && !t.declinedBy(d.ID) && !slices.ContainsFunc(pool, func(c candidate) bool { return c.d == d }) &&
				geo.Distance(d.Pos, t.Pickup.Pt) < visitorReach {
				pool = append(pool, candidate{d: d})
			}
		}
	}
	if len(pool) == 0 {
		return nil
	}
	origin := func(c candidate) uint32 {
		if c.b2b {
			return c.d.Trip.Dropoff.Node
		}
		return e.node(c.d)
	}
	dist := make([]float64, len(pool))
	for i, c := range pool {
		dist[i] = e.g.PlanarDistance(origin(c), t.Pickup.Node)
	}
	idx := make([]int, len(pool))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		if dist[idx[a]] != dist[idx[b]] {
			return dist[idx[a]] < dist[idx[b]]
		}
		return pool[idx[a]].d.Num < pool[idx[b]].d.Num
	})
	if len(idx) > e.cfgD.MaxCandidates {
		keep := append([]int(nil), idx[:e.cfgD.MaxCandidates]...)
		for _, j := range idx[e.cfgD.MaxCandidates:] {
			if !pool[j].d.Simulated {
				keep = append(keep, j)
			}
		}
		idx = keep
	}
	chosen := make([]candidate, len(idx))
	targets := make([]uint32, len(idx))
	for i, j := range idx {
		chosen[i] = pool[j]
		targets[i] = origin(pool[j])
	}
	f := e.Factor()
	times := e.router.TimesTo(t.Pickup.Node, targets, float64(e.cfgD.MaxPickupS)/f)
	out := chosen[:0]
	for i, c := range chosen {
		if math.IsInf(times[i], 1) {
			continue
		}
		eta := times[i] * f
		if c.b2b {
			eta += e.remaining(c.d) + e.cfgD.DropoffDwellS
		} else {
			eta += geo.Distance(c.d.Pos, e.g.Point(e.node(c.d))) / 6
		}
		if eta > float64(e.cfgD.MaxPickupS) {
			continue
		}
		c.eta = eta
		out = append(out, c)
	}
	return out
}

func (e *Engine) dispatch() {
	if len(e.open) == 0 {
		return
	}
	if !e.batch {
		pending := append([]*Trip(nil), e.open...)
		for _, t := range pending {
			if t.State == Searching {
				e.dispatchOne(t)
			}
		}
		return
	}
	trips := append([]*Trip(nil), e.open[:min(len(e.open), e.cfgD.MaxBatch)]...)
	type row struct {
		t     *Trip
		cands []candidate
	}
	var rows []row
	col := map[uint32]int{}
	var cols []*Driver
	for _, t := range trips {
		cs := e.candidates(t)
		if len(cs) == 0 {
			continue
		}
		rows = append(rows, row{t, cs})
		for _, c := range cs {
			if _, ok := col[c.d.Num]; !ok {
				col[c.d.Num] = len(cols)
				cols = append(cols, c.d)
			}
		}
	}
	if len(rows) == 0 {
		return
	}
	cost := make([][]float64, len(rows))
	eta := make([][]float64, len(rows))
	for i, r := range rows {
		cost[i] = make([]float64, len(cols))
		eta[i] = make([]float64, len(cols))
		for j := range cost[i] {
			cost[i][j] = unmatched
		}
		age := math.Min(float64(e.now-r.t.RequestedAt)/1000, 600)
		for _, c := range r.cands {
			j := col[c.d.Num]
			cost[i][j] = c.eta - e.cfgD.AgeWeight*age - e.headStart(c.d)
			eta[i][j] = c.eta
		}
	}
	for i, j := range Hungarian(cost) {
		if j < 0 || cost[i][j] >= unmatched/2 {
			continue
		}
		e.offer(rows[i].t, cols[j], eta[i][j])
	}
}

func (e *Engine) headStart(d *Driver) float64 {
	if d.Simulated {
		return 0
	}
	return e.cfgD.VisitorHeadStartS
}

func (e *Engine) dispatchOne(t *Trip) {
	if t.State != Searching {
		return
	}
	cs := e.candidates(t)
	if len(cs) == 0 {
		return
	}
	best := cs[0]
	for _, c := range cs[1:] {
		a, b := c.eta-e.headStart(c.d), best.eta-e.headStart(best.d)
		if a < b || (a == b && c.d.Num < best.d.Num) {
			best = c
		}
	}
	e.offer(t, best.d, best.eta)
}

func (e *Engine) offer(t *Trip, d *Driver, eta float64) {
	b2b := d.State == OnTrip
	o := &Offer{
		ID: e.nextID("O", &e.seqOffer), Trip: t, Driver: d, SentAt: e.now,
		ExpiresAt: e.now + int64(e.cfgD.OfferTimeoutS)*1000, ETA: eta, B2B: b2b,
	}
	e.removeOpen(t)
	t.State = Offering
	t.Offer = o
	t.Offers++
	if t.OfferedAt == 0 {
		t.OfferedAt = e.now
	}
	d.Offer = o
	if b2b {
		e.unindexB2B(d)
	} else {
		e.unindexIdle(d)
		e.setState(d, Offered)
	}
	e.schedule(o.ExpiresAt, tOfferExpire, o)
	e.stats.Offers++
	v := e.view(t)
	e.emit(Event{Kind: OfferSent, Offer: e.offerView(o), Trip: &v, DriverID: d.ID, RiderID: t.RiderID})
}

func Hungarian(a [][]float64) []int {
	n := len(a)
	if n == 0 {
		return nil
	}
	m := len(a[0])
	if n > m {
		t := make([][]float64, m)
		for j := range t {
			t[j] = make([]float64, n)
			for i := 0; i < n; i++ {
				t[j][i] = a[i][j]
			}
		}
		colOf := Hungarian(t)
		out := make([]int, n)
		for i := range out {
			out[i] = -1
		}
		for j, i := range colOf {
			if i >= 0 {
				out[i] = j
			}
		}
		return out
	}
	u := make([]float64, n+1)
	v := make([]float64, m+1)
	p := make([]int, m+1)
	way := make([]int, m+1)
	minv := make([]float64, m+1)
	used := make([]bool, m+1)
	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		for j := range minv {
			minv[j] = math.Inf(1)
			used[j] = false
		}
		for {
			used[j0] = true
			i0, delta, j1 := p[j0], math.Inf(1), 0
			for j := 1; j <= m; j++ {
				if used[j] {
					continue
				}
				if cur := a[i0-1][j-1] - u[i0] - v[j]; cur < minv[j] {
					minv[j], way[j] = cur, j0
				}
				if minv[j] < delta {
					delta, j1 = minv[j], j
				}
			}
			for j := 0; j <= m; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for j0 != 0 {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
		}
	}
	out := make([]int, n)
	for i := range out {
		out[i] = -1
	}
	for j := 1; j <= m; j++ {
		if p[j] != 0 {
			out[p[j]-1] = j - 1
		}
	}
	return out
}
