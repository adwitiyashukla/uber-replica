package sim

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"sort"

	"github.com/adwitiyashukla/uber-replica/internal/city"
	"github.com/adwitiyashukla/uber-replica/internal/config"
	"github.com/adwitiyashukla/uber-replica/internal/engine"
	"github.com/adwitiyashukla/uber-replica/internal/geo"
	"github.com/adwitiyashukla/uber-replica/internal/roads"
)

type phase uint8

const (
	idle phase = iota
	toPickup
	waiting
	riding
	finishing
	repositioning
)

type driver struct {
	id       string
	manual   bool
	online   bool
	product  engine.Product
	pos      geo.Point
	heading  float64
	node     uint32
	track    *roads.Track
	progress float64
	phase    phase
	tripID   string
	offerID  string
	decideAt int64
	accept   bool
	actAt    int64
	nextLoc  int64
	repoAt   int64
	since    int64
}

type rider struct {
	id       string
	tripID   string
	cancelAt int64
	maxETA   float64
	done     bool
}

type Counters struct {
	Sessions     int `json:"sessions"`
	Lost         int `json:"lost"`
	Requested    int `json:"requested"`
	Skipped      int `json:"skipped"`
	Spawned      int `json:"spawned"`
	Retired      int `json:"retired"`
	RiderCancels int `json:"riderCancels"`
}

type Sim struct {
	cfg      config.Sim
	c        *city.City
	g        *roads.Graph
	e        *engine.Engine
	rng      *rand.Rand
	visitors *rand.Rand
	router   *roads.Router

	drivers []*driver
	byID    map[string]*driver
	riders  []*rider
	riderBy map[string]*rider

	day       int64
	next      int
	last      int64
	lastFleet int64
	started   bool
	seqDriver int
	seqRider  int
	zoneCum   [24][]float64
	zoneList  []uint16
	recent    [16]int
	recentMin int64
	centroids map[uint16]geo.Point
	idleIn    map[uint16]int

	Counters
}

func New(cfg config.Config, c *city.City, e *engine.Engine) *Sim {
	s := &Sim{
		cfg:       cfg.Sim,
		c:         c,
		g:         c.Graph,
		e:         e,
		rng:       rand.New(rand.NewSource(cfg.Sim.Seed)),
		visitors:  rand.New(rand.NewSource(cfg.Sim.Seed + 1)),
		router:    roads.NewRouter(c.Graph),
		byID:      map[string]*driver{},
		riderBy:   map[string]*rider{},
		centroids: map[uint16]geo.Point{},
		idleIn:    map[uint16]int{},
	}
	s.zoneList = append(s.zoneList, c.Graph.Zones()...)
	sort.Slice(s.zoneList, func(i, j int) bool { return s.zoneList[i] < s.zoneList[j] })
	for _, z := range s.zoneList {
		if zz, ok := c.Zone(z); ok {
			s.centroids[z] = geo.Point{Lat: zz.Lat, Lng: zz.Lng}
		}
	}
	for h := 0; h < 24; h++ {
		acc := 0.0
		cum := make([]float64, len(s.zoneList))
		for i, z := range s.zoneList {
			acc += c.Forecast(z, h) + 0.5
			cum[i] = acc
		}
		s.zoneCum[h] = cum
	}
	return s
}

func daySeconds(now int64) float64 { return math.Mod(float64(now)/1000, 86400) }

func hourOf(now int64) int { return int(daySeconds(now)/3600) % 24 }

func (s *Sim) uniform(lo, hi float64) float64 { return lo + (hi-lo)*s.rng.Float64() }

func (s *Sim) Start(now int64) {
	s.last = now
	s.day = now / 86400000
	sec := daySeconds(now)
	s.next = sort.Search(len(s.c.Demand), func(i int) bool { return float64(s.c.Demand[i].T) >= sec })
	if s.cfg.Enabled {
		s.manageFleet(now)
	}
	s.lastFleet = now
}

func (s *Sim) Step(now int64) {
	dt := math.Max(0, float64(now-s.last)/1000)
	s.last = now
	if s.cfg.Enabled {
		s.spawnDemand(now)
		if now-s.lastFleet >= 30000 {
			s.manageFleet(now)
			s.lastFleet = now
		}
	}
	f := s.c.HourFactor(daySeconds(now))
	for _, d := range s.drivers {
		if d.online {
			s.drive(d, now, dt, f)
			s.act(d, now)
		}
	}
	s.handleRiders(now)
}

func (s *Sim) drive(d *driver, now int64, dt, factor float64) {
	if d.track == nil {
		return
	}
	d.progress += dt / factor
	end := d.progress >= d.track.Duration()
	if end {
		d.progress = d.track.Duration()
	}
	if end || now >= d.nextLoc {
		d.pos, d.heading = d.track.At(d.progress)
		_ = s.e.Location(d.id, d.pos, d.heading)
		d.nextLoc = now + int64(s.cfg.LocationEveryS*1000)
	}
	if !end {
		return
	}
	d.node = d.track.Nodes[len(d.track.Nodes)-1]
	d.track = nil
	switch d.phase {
	case toPickup:
		if err := s.e.Arrive(d.id, d.tripID); err != nil {
			d.phase = idle
			return
		}
		d.phase = waiting
		d.actAt = now + int64(s.uniform(s.cfg.BoardMinS, s.cfg.BoardMaxS)*1000)
		if d.manual {
			d.actAt = now + int64(s.cfg.ManualTimeoutS*1000)
		}
	case riding:
		d.phase = finishing
		d.actAt = now + int64(s.cfg.DropoffS*1000)
		if d.manual {
			d.actAt = now + int64(s.cfg.ManualTimeoutS*1000)
		}
	case repositioning:
		d.phase = idle
		d.repoAt = now + int64(s.uniform(1, 2)*s.cfg.RepositionAfterS*1000)
	}
}

func (s *Sim) act(d *driver, now int64) {
	if d.offerID != "" && !d.manual && now >= d.decideAt {
		id := d.offerID
		d.offerID = ""
		if d.accept {
			_ = s.e.Accept(d.id, id)
		} else {
			_ = s.e.Decline(d.id, id)
		}
	}
	switch d.phase {
	case waiting:
		if now >= d.actAt {
			if err := s.e.StartTrip(d.id, d.tripID); err != nil {
				d.phase = idle
			}
		}
	case finishing:
		if now >= d.actAt {
			if err := s.e.Complete(d.id, d.tripID); err != nil {
				d.phase = idle
			}
		}
	case idle:
		if !d.manual && d.track == nil && d.offerID == "" && now >= d.repoAt {
			s.reposition(d, now)
		}
	}
}

func (s *Sim) Observe(ev engine.Event) {
	switch ev.Kind {
	case engine.DriverOnline:
		d := s.byID[ev.DriverID]
		if d == nil {
			d = &driver{id: ev.DriverID, manual: true, product: ev.Driver.Product}
			s.drivers = append(s.drivers, d)
			s.byID[d.id] = d
		}
		d.online, d.phase, d.track = true, idle, nil
		d.pos = geo.Point{Lat: ev.Driver.Lat, Lng: ev.Driver.Lng}
		d.node = s.g.Nearest(d.pos)
		d.since = ev.At
		d.repoAt = ev.At + int64(s.uniform(0.2, 1)*s.cfg.RepositionAfterS*1000)
	case engine.DriverOffline:
		if d := s.byID[ev.DriverID]; d != nil {
			d.online, d.track, d.phase, d.offerID, d.tripID = false, nil, idle, "", ""
		}
	case engine.OfferSent:
		if d := s.byID[ev.DriverID]; d != nil && !d.manual {
			d.offerID = ev.Offer.ID
			d.decideAt = ev.At + int64(s.uniform(s.cfg.DecideMinS, s.cfg.DecideMaxS)*1000)
			d.accept = s.rng.Float64() < s.cfg.AcceptRate
		}
	case engine.OfferClosed:
		if d := s.byID[ev.DriverID]; d != nil && d.offerID == ev.Offer.ID {
			d.offerID = ""
		}
	case engine.DriverRouted:
		d := s.byID[ev.DriverID]
		if d == nil {
			return
		}
		d.track = withLeadIn(ev.Route.Track, s.here(d))
		d.progress = 0
		if ev.Trip != nil {
			d.tripID = ev.Trip.ID
		}
		if ev.Route.Purpose == "pickup" {
			d.phase = toPickup
		} else {
			d.phase = riding
		}
	case engine.TripAccepted:
		if r := s.riderBy[ev.RiderID]; r != nil && r.tripID == ev.Trip.ID {
			r.cancelAt = 0
			if ev.Trip.ETA > r.maxETA {
				r.cancelAt = ev.At + int64(s.uniform(10, 30)*1000)
			}
		}
	case engine.TripReopened:
		if r := s.riderBy[ev.RiderID]; r != nil && r.tripID == ev.Trip.ID {
			r.cancelAt = ev.At + int64(s.uniform(s.cfg.PatienceMinS, s.cfg.PatienceMaxS)*1000)
		}
	case engine.TripCancelled, engine.TripCompleted:
		if r := s.riderBy[ev.RiderID]; r != nil && r.tripID == ev.Trip.ID {
			r.done = true
			delete(s.riderBy, r.id)
		}
		if ev.Trip.Driver == nil {
			return
		}
		if d := s.byID[ev.Trip.Driver.ID]; d != nil && d.tripID == ev.Trip.ID {
			if ev.Kind == engine.TripCancelled && d.track != nil {
				_ = s.e.Location(d.id, s.here(d), d.heading)
				d.track = nil
			}
			d.tripID, d.phase = "", idle
			d.repoAt = ev.At + int64(s.uniform(0.5, 1.5)*s.cfg.RepositionAfterS*1000)
		}
	}
}

func (s *Sim) handleRiders(now int64) {
	keep := s.riders[:0]
	for _, r := range s.riders {
		if r.done {
			continue
		}
		if r.cancelAt > 0 && now >= r.cancelAt {
			r.cancelAt = 0
			if _, err := s.e.Cancel(r.id, r.tripID); err == nil {
				s.RiderCancels++
			}
		}
		keep = append(keep, r)
	}
	for i := len(keep); i < len(s.riders); i++ {
		s.riders[i] = nil
	}
	s.riders = keep
}

func (s *Sim) spawnDemand(now int64) {
	sec := daySeconds(now)
	if day := now / 86400000; day != s.day {
		s.day, s.next = day, 0
	}
	for s.next < len(s.c.Demand) && float64(s.c.Demand[s.next].T) <= sec {
		req := s.c.Demand[s.next]
		s.next++
		n := int(s.cfg.DemandScale)
		if s.rng.Float64() < s.cfg.DemandScale-float64(n) {
			n++
		}
		for i := 0; i < n; i++ {
			s.noteRequest(now)
			s.newRider(req, now)
		}
	}
}

func (s *Sim) pickProduct() engine.Product {
	r := s.rng.Float64()
	switch {
	case r < s.cfg.XLShare:
		return engine.XL
	case r < s.cfg.XLShare+s.cfg.ComfortShare:
		return engine.Comfort
	}
	return engine.Standard
}

func (s *Sim) newRider(req city.Request, now int64) {
	pus, dos := s.g.ZoneNodes(req.PU), s.g.ZoneNodes(req.DO)
	if len(pus) == 0 || len(dos) == 0 {
		s.Skipped++
		return
	}
	a, b := pus[s.rng.Intn(len(pus))], dos[s.rng.Intn(len(dos))]
	if a == b {
		s.Skipped++
		return
	}
	s.seqRider++
	id := fmt.Sprintf("S%07d", s.seqRider)
	s.Sessions++
	q, err := s.e.Quote(id, s.g.Point(a), s.g.Point(b))
	if err != nil {
		s.Skipped++
		return
	}
	product := s.pickProduct()
	if willing := s.willingness(req); q.Surge > willing && s.rng.Float64() > math.Exp(-s.cfg.PriceSensitivity*(q.Surge-willing)) {
		s.Lost++
		return
	}
	v, err := s.e.Request(engine.RideRequest{RiderID: id, Token: q.Options[product].Token, Simulated: true})
	if err != nil {
		s.Skipped++
		return
	}
	s.Requested++
	r := &rider{
		id: id, tripID: v.ID,
		cancelAt: now + int64(s.uniform(s.cfg.PatienceMinS, s.cfg.PatienceMaxS)*1000),
		maxETA:   s.cfg.MaxAcceptableETAS * s.uniform(0.7, 1.3),
	}
	s.riders = append(s.riders, r)
	s.riderBy[id] = r
}

func (s *Sim) willingness(req city.Request) float64 {
	f := s.c.Cal.Fare
	model := math.Max(f.Base+f.PerMile*float64(req.Miles)+f.PerMinute*float64(req.Trip)/60, f.Min)
	if req.Fare <= 0 || model <= 0 {
		return 1
	}
	return math.Min(math.Max(float64(req.Fare)/model, 1), 3)
}

func (s *Sim) Target(now int64) int {
	x := math.Mod(daySeconds(now)+600, 86400)/3600 - 0.5
	if x < 0 {
		x += 24
	}
	h := int(x) % 24
	frac := x - math.Floor(x)
	hours := s.c.Cal.Real.Hours
	lerp := func(a, b float64) float64 { return a + (b-a)*frac }
	rate := lerp(s.c.TypicalRequests(h), s.c.TypicalRequests(h+1)) / 60 * s.cfg.DemandScale
	if seen := s.recentRate(now); rate > 0 && seen > rate {
		rate *= 1 + s.cfg.SupplyResponse*(seen/rate-1)
	}
	trip := lerp(hours[h].TripP50, hours[(h+1)%24].TripP50) / 60
	need := (float64(s.cfg.FleetBase)*s.cfg.DemandScale + rate*(trip+5)) * s.cfg.FleetScale
	return max(10, int(math.Ceil(need)))
}

func (s *Sim) noteRequest(now int64) {
	n := int64(len(s.recent))
	m := now / 60000
	if m-s.recentMin >= n {
		s.recent, s.recentMin = [16]int{}, m
	}
	for ; s.recentMin < m; s.recentMin++ {
		s.recent[(s.recentMin+1)%n] = 0
	}
	s.recent[m%n]++
}

func (s *Sim) recentRate(now int64) float64 {
	n := int64(len(s.recent))
	m := now / 60000
	sum := 0
	for k := m - n + 1; k < m; k++ {
		if k <= s.recentMin && k > s.recentMin-n {
			sum += s.recent[k%n]
		}
	}
	return float64(sum) / float64(n-1)
}

func (s *Sim) manageFleet(now int64) {
	target := s.Target(now)
	online := 0
	for k := range s.idleIn {
		delete(s.idleIn, k)
	}
	for _, d := range s.drivers {
		if d.manual || !d.online {
			continue
		}
		online++
		if d.phase == idle || d.phase == repositioning {
			s.idleIn[s.g.Zone[d.node]]++
		}
	}
	switch {
	case online < target:
		add := target - online
		if s.started {
			add = min(add, max(5, target/25))
		}
		for i := 0; i < add; i++ {
			s.spawn(now)
		}
	case float64(online) > float64(target)*1.05:
		remove := online - target
		for _, d := range s.drivers {
			if remove == 0 {
				break
			}
			if d.manual || !d.online || d.offerID != "" || (d.phase != idle && d.phase != repositioning) {
				continue
			}
			if err := s.e.DriverOffline(d.id); err == nil {
				remove--
				s.Retired++
			}
		}
	}
	keep := s.drivers[:0]
	for _, d := range s.drivers {
		if d.online || d.manual {
			keep = append(keep, d)
		} else {
			delete(s.byID, d.id)
		}
	}
	for i := len(keep); i < len(s.drivers); i++ {
		s.drivers[i] = nil
	}
	s.drivers = keep
	s.started = true
}

func (s *Sim) sampleZone(h int) uint16 {
	cum := s.zoneCum[h]
	x := s.rng.Float64() * cum[len(cum)-1]
	i := sort.SearchFloat64s(cum, x)
	return s.zoneList[min(i, len(cum)-1)]
}

func (s *Sim) fleetProduct() engine.Product {
	r := s.rng.Float64()
	switch {
	case r < s.cfg.XLFleetShare:
		return engine.XL
	case r < s.cfg.XLFleetShare+s.cfg.ComfortFleetShare:
		return engine.Comfort
	}
	return engine.Standard
}

func (s *Sim) spawn(now int64) {
	nodes := s.g.ZoneNodes(s.sampleZone(hourOf(now)))
	n := nodes[s.rng.Intn(len(nodes))]
	s.seqDriver++
	id := fmt.Sprintf("D%05d", s.seqDriver)
	product := s.fleetProduct()
	info := driverInfo(s.rng, id, product)
	d := &driver{
		id: id, product: product, pos: s.g.Point(n), node: n, online: true, since: now,
		repoAt: now + int64(s.uniform(0.2, 1)*s.cfg.RepositionAfterS*1000),
	}
	s.drivers = append(s.drivers, d)
	s.byID[id] = d
	if _, err := s.e.DriverOnline(info, d.pos); err != nil {
		d.online = false
		return
	}
	s.Spawned++
}

func (s *Sim) reposition(d *driver, now int64) {
	h := hourOf(now)
	here := s.g.Zone[d.node]
	var zones []uint16
	var weights []float64
	total := 0.0
	for _, z := range s.zoneList {
		c, ok := s.centroids[z]
		if !ok || geo.Distance(c, s.here(d)) > s.cfg.RepositionRadiusM {
			continue
		}
		w := (s.c.Forecast(z, h) + 0.1) / float64(1+s.idleIn[z])
		zones = append(zones, z)
		weights = append(weights, w)
		total += w
	}
	d.repoAt = now + int64(s.uniform(1, 2)*s.cfg.RepositionAfterS*1000)
	if total == 0 {
		return
	}
	x := s.rng.Float64() * total
	pick := zones[len(zones)-1]
	for i, w := range weights {
		if x < w {
			pick = zones[i]
			break
		}
		x -= w
	}
	if pick == here {
		return
	}
	nodes := s.g.ZoneNodes(pick)
	dest := nodes[s.rng.Intn(len(nodes))]
	path, ok := s.router.Route(d.node, dest)
	if !ok || len(path.Edges) == 0 {
		return
	}
	s.idleIn[here]--
	s.idleIn[pick]++
	d.track = withLeadIn(s.g.Track(path), s.here(d))
	d.progress = 0
	d.phase = repositioning
}

func (s *Sim) here(d *driver) geo.Point {
	if d.track != nil {
		d.pos, d.heading = d.track.At(d.progress)
	}
	return d.pos
}

func withLeadIn(tr *roads.Track, pos geo.Point) *roads.Track {
	gap := geo.Distance(pos, tr.Pts[0])
	if gap < 3 {
		return tr
	}
	lead := gap / 6
	out := &roads.Track{Nodes: tr.Nodes, Edges: tr.Edges}
	out.Pts = append([]geo.Point{pos}, tr.Pts...)
	out.T = make([]float64, len(tr.T)+1)
	out.M = make([]float64, len(tr.M)+1)
	for i := range tr.T {
		out.T[i+1] = tr.T[i] + lead
		out.M[i+1] = tr.M[i] + gap
	}
	return out
}

func (s *Sim) OnlineDrivers() int {
	n := 0
	for _, d := range s.drivers {
		if d.online && !d.manual {
			n++
		}
	}
	return n
}

func (s *Sim) WaitingRiders() int { return len(s.riderBy) }

func (s *Sim) SpawnPoint(now int64) geo.Point {
	h := hourOf(now)
	zones := slices.Clone(s.zoneList)
	sort.SliceStable(zones, func(a, b int) bool { return s.c.Forecast(zones[a], h) > s.c.Forecast(zones[b], h) })
	zones = zones[:min(len(zones), 12)]
	nodes := s.g.ZoneNodes(zones[s.visitors.Intn(len(zones))])
	return s.g.Point(nodes[s.visitors.Intn(len(nodes))])
}
