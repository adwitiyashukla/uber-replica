package engine

import (
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/uber/h3-go/v4"

	"github.com/adwitiyashukla/uber-replica/internal/city"
	"github.com/adwitiyashukla/uber-replica/internal/config"
	"github.com/adwitiyashukla/uber-replica/internal/geo"
	"github.com/adwitiyashukla/uber-replica/internal/roads"
)

const (
	indexRes      = 9
	keepFinished  = 30 * 60 * 1000
	keepOffline   = 60 * 60 * 1000
	serviceRadius = 600.0
)

type timerKind uint8

const (
	tDispatch timerKind = iota
	tSurge
	tMinute
	tOfferExpire
	tRequestTimeout
)

type timer struct {
	at   int64
	seq  uint64
	kind timerKind
	ref  any
}

type timerHeap []timer

func (h timerHeap) less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}

func (h *timerHeap) push(t timer) {
	*h = append(*h, t)
	s := *h
	for i := len(s) - 1; i > 0; {
		p := (i - 1) / 2
		if !s.less(i, p) {
			break
		}
		s[p], s[i] = s[i], s[p]
		i = p
	}
}

func (h *timerHeap) pop() timer {
	s := *h
	top := s[0]
	n := len(s) - 1
	s[0] = s[n]
	s = s[:n]
	for i := 0; ; {
		l, r, m := 2*i+1, 2*i+2, i
		if l < n && s.less(l, m) {
			m = l
		}
		if r < n && s.less(r, m) {
			m = r
		}
		if m == i {
			break
		}
		s[m], s[i] = s[i], s[m]
		i = m
	}
	*h = s
	return top
}

type Engine struct {
	cfgD   config.Dispatch
	cfgP   config.Pricing
	cfgS   config.Surge
	city   *city.City
	g      *roads.Graph
	router *roads.Router
	secret []byte

	now   int64
	start int64

	drivers map[string]*Driver
	byNum   []*Driver
	trips   map[string]*Trip
	open    []*Trip
	refs    map[string]*Trip
	active  map[string]*Trip
	idle    map[h3.Cell][]*Driver
	b2b     map[h3.Cell][]*Driver
	humans  []*Driver
	parked  [2][]parking
	retired [2]int64
	counts  [6]int

	batch      bool
	backToBack bool

	surge    *surge
	timers   timerHeap
	out      []Event
	finished []*Trip
	stats    *Stats
	books    map[string]int64

	prefix                                            string
	seqTrip, seqOffer, seqEvent, seqJournal, seqTimer uint64
}

func New(cfg config.Config, c *city.City, secret []byte) *Engine {
	return &Engine{
		cfgD:       cfg.Dispatch,
		cfgP:       cfg.Pricing,
		cfgS:       cfg.Surge,
		city:       c,
		g:          c.Graph,
		router:     roads.NewRouter(c.Graph),
		secret:     secret,
		drivers:    map[string]*Driver{},
		trips:      map[string]*Trip{},
		refs:       map[string]*Trip{},
		active:     map[string]*Trip{},
		idle:       map[h3.Cell][]*Driver{},
		b2b:        map[h3.Cell][]*Driver{},
		batch:      cfg.Dispatch.Policy == "batch",
		backToBack: cfg.Dispatch.BackToBack,
		surge:      newSurge(cfg.Surge, c.Graph),
		stats:      newStats(),
		books:      map[string]int64{},
	}
}

func (e *Engine) Start(now int64) {
	e.now, e.start = now, now
	e.schedule(now+int64(e.cfgD.BatchWindowMs), tDispatch, nil)
	e.schedule(now+int64(e.cfgS.TickS)*1000, tSurge, nil)
	e.schedule((now/60000+1)*60000, tMinute, nil)
}

func (e *Engine) Now() int64 { return e.now }

func (e *Engine) City() *city.City { return e.city }

func (e *Engine) DaySeconds() float64 {
	return math.Mod(float64(e.now)/1000, 86400)
}

func (e *Engine) Factor() float64 { return e.city.HourFactor(e.DaySeconds()) }

func (e *Engine) Policy() (string, bool) {
	if e.batch {
		return "batch", e.backToBack
	}
	return "greedy", e.backToBack
}

func (e *Engine) SetPolicy(policy string, backToBack bool) error {
	if policy != "batch" && policy != "greedy" {
		return fmt.Errorf("unknown policy %q", policy)
	}
	e.batch = policy == "batch"
	if e.backToBack && !backToBack {
		for _, d := range e.byNum {
			e.unindexB2B(d)
		}
	}
	e.backToBack = backToBack
	return nil
}

func (e *Engine) schedule(at int64, kind timerKind, ref any) {
	e.seqTimer++
	e.timers.push(timer{at: at, seq: e.seqTimer, kind: kind, ref: ref})
}

func (e *Engine) Advance(now int64) {
	for len(e.timers) > 0 && e.timers[0].at <= now {
		t := e.timers.pop()
		if t.at > e.now {
			e.now = t.at
		}
		e.fire(t)
	}
	if now > e.now {
		e.now = now
	}
}

func (e *Engine) fire(t timer) {
	switch t.kind {
	case tDispatch:
		e.dispatch()
		e.schedule(e.now+int64(e.cfgD.BatchWindowMs), tDispatch, nil)
	case tSurge:
		if e.cfgS.Enabled {
			if changed := e.surge.tick(float64(e.cfgS.TickS)); len(changed) > 0 {
				e.emit(Event{Kind: SurgeChanged, Surge: changed})
			}
		}
		e.schedule(e.now+int64(e.cfgS.TickS)*1000, tSurge, nil)
	case tMinute:
		e.stats.closeMinute(e)
		e.evict()
		e.schedule(e.now+60000, tMinute, nil)
	case tOfferExpire:
		o := t.ref.(*Offer)
		if o.Driver.Offer == o {
			e.stats.Expired++
			e.closeOffer(o, "expired")
		}
	case tRequestTimeout:
		tr := t.ref.(*Trip)
		if tr.State.Open() {
			if tr.Offer != nil {
				e.withdrawOffer(tr.Offer)
			} else {
				e.removeOpen(tr)
			}
			e.cancelTrip(tr, "no_drivers", 0)
		}
	}
}

func (e *Engine) Drain() []Event {
	out := e.out
	e.out = nil
	return out
}

func (e *Engine) emit(ev Event) {
	e.seqEvent++
	ev.Seq = e.seqEvent
	ev.At = e.now
	e.out = append(e.out, ev)
}

func (e *Engine) nextID(kind string, seq *uint64) string {
	*seq++
	return fmt.Sprintf("%s%s%06d", kind, e.prefix, *seq)
}

func (e *Engine) SetPrefix(p string) { e.prefix = p }

func (e *Engine) place(n uint32) Place {
	return Place{Node: n, Name: e.g.NodeName(n), Zone: e.g.Zone[n], Pt: e.g.Point(n)}
}

func (e *Engine) node(d *Driver) uint32 {
	if d.moved {
		d.Node, d.moved = e.g.Nearest(d.Pos), false
	}
	return d.Node
}

func named(p Place, name string) Place {
	if name != "" {
		p.Name = name
	}
	return p
}

func (e *Engine) inService(p geo.Point) (uint32, bool) {
	n := e.g.NearestCurb(p)
	return n, geo.Distance(p, e.g.Point(n)) <= serviceRadius
}

func (e *Engine) setState(d *Driver, s DriverState) {
	old := d.State
	if old == s {
		return
	}
	e.counts[old]--
	e.counts[s]++
	if old.Busy() && !s.Busy() {
		d.BusyMs += e.now - d.busyFrom
	}
	if s.Busy() && !old.Busy() {
		d.busyFrom = e.now
	}
	if s == Offline {
		d.OnlineMs += e.now - d.OnlineAt
		d.offAt = e.now
		k := kind(d.Simulated)
		e.parked[k] = append(e.parked[k], parking{d, e.now})
	}
	if !d.Simulated && old == Offline {
		e.humans = append(e.humans, d)
	}
	if !d.Simulated && s == Offline {
		e.humans = slices.DeleteFunc(e.humans, func(h *Driver) bool { return h == d })
	}
	d.State = s
}

func (e *Engine) indexIdle(d *Driver) {
	if d.inIdle {
		return
	}
	c := geo.Cell(d.Pos, indexRes)
	d.cell, d.cellPt, d.cellPos, d.inIdle = c, d.Pos, len(e.idle[c]), true
	e.idle[c] = append(e.idle[c], d)
}

func (e *Engine) unindexIdle(d *Driver) {
	if !d.inIdle {
		return
	}
	s := e.idle[d.cell]
	last := len(s) - 1
	s[d.cellPos] = s[last]
	s[d.cellPos].cellPos = d.cellPos
	s = s[:last]
	if len(s) == 0 {
		delete(e.idle, d.cell)
	} else {
		e.idle[d.cell] = s
	}
	d.inIdle = false
}

func (e *Engine) indexB2B(d *Driver) {
	if d.inB2B || !e.backToBack || d.Trip == nil || d.Next != nil || d.Offer != nil || d.GoOffline {
		return
	}
	c := geo.Cell(d.Trip.Dropoff.Pt, indexRes)
	d.b2bCell, d.b2bPos, d.inB2B = c, len(e.b2b[c]), true
	e.b2b[c] = append(e.b2b[c], d)
}

func (e *Engine) unindexB2B(d *Driver) {
	if !d.inB2B {
		return
	}
	s := e.b2b[d.b2bCell]
	last := len(s) - 1
	s[d.b2bPos] = s[last]
	s[d.b2bPos].b2bPos = d.b2bPos
	s = s[:last]
	if len(s) == 0 {
		delete(e.b2b, d.b2bCell)
	} else {
		e.b2b[d.b2bCell] = s
	}
	d.inB2B = false
}

func (e *Engine) insertOpen(t *Trip) {
	i := sort.Search(len(e.open), func(i int) bool { return e.open[i].seq >= t.seq })
	if i < len(e.open) && e.open[i] == t {
		return
	}
	e.open = append(e.open, nil)
	copy(e.open[i+1:], e.open[i:])
	e.open[i] = t
}

func (e *Engine) removeOpen(t *Trip) {
	i := sort.Search(len(e.open), func(i int) bool { return e.open[i].seq >= t.seq })
	if i < len(e.open) && e.open[i] == t {
		e.open = append(e.open[:i], e.open[i+1:]...)
	}
}

func (e *Engine) remaining(d *Driver) float64 {
	if d.route == nil {
		return 0
	}
	return math.Max(0, d.route.Duration()-d.routeSec) * e.Factor()
}

func (e *Engine) routeTo(d *Driver, node uint32, purpose string) {
	path, ok := e.router.Route(e.node(d), node)
	if !ok {
		path = roads.Path{Nodes: []uint32{node}}
	}
	tr := e.g.Track(path)
	d.route, d.routeSec, d.routeSeg = tr, 0, 0
	var tv *TripView
	if d.Trip != nil {
		v := e.view(d.Trip)
		tv = &v
	}
	e.emit(Event{Kind: DriverRouted, DriverID: d.ID, Trip: tv, Route: &Route{DriverID: d.ID, Purpose: purpose, Path: path, Track: tr}})
}

func (e *Engine) DriverOnline(info DriverInfo, pos geo.Point) (DriverView, error) {
	d := e.drivers[info.ID]
	if d != nil && d.State != Offline {
		return DriverView{}, ErrDriverOnline
	}
	node, ok := e.inService(pos)
	if !ok {
		return DriverView{}, ErrOutsideService
	}
	if d == nil {
		if d = e.reuse(info.Simulated); d == nil {
			d = &Driver{Num: uint32(len(e.byNum))}
			e.byNum = append(e.byNum, d)
			e.counts[Offline]++
		}
		e.drivers[info.ID] = d
	}
	d.DriverInfo = info
	d.Pos, d.Node, d.Heading, d.moved = pos, node, 0, false
	d.OnlineAt, d.GoOffline = e.now, false
	e.setState(d, Idle)
	e.indexIdle(d)
	card := e.card(d)
	e.emit(Event{Kind: DriverOnline, DriverID: d.ID, Driver: &card})
	return e.driverView(d), nil
}

func (e *Engine) DriverOffline(id string) error {
	d := e.drivers[id]
	if d == nil {
		return ErrUnknownDriver
	}
	if d.State == Offline {
		return nil
	}
	if d.Offer != nil {
		e.closeOffer(d.Offer, "driver_offline")
	}
	if d.Next != nil {
		e.reopen(d.Next, d)
		d.Next = nil
	}
	switch d.State {
	case Idle, Offered:
		e.unindexIdle(d)
	case EnRoute, AtPickup:
		t := d.Trip
		d.Trip, d.route = nil, nil
		e.reopen(t, d)
	case OnTrip:
		d.GoOffline = true
		e.unindexB2B(d)
		return nil
	}
	e.goOffline(d)
	return nil
}

type parking struct {
	d  *Driver
	at int64
}

func kind(simulated bool) int {
	if simulated {
		return 1
	}
	return 0
}

func (e *Engine) reuse(simulated bool) *Driver {
	k := kind(simulated)
	q := e.parked[k]
	defer func() { e.parked[k] = q }()
	for len(q) > 0 {
		p := q[0]
		if p.d.State != Offline || p.d.offAt != p.at {
			q = q[1:]
			continue
		}
		if e.now-p.at < keepOffline {
			return nil
		}
		q = q[1:]
		d := p.d
		delete(e.drivers, d.ID)
		e.retired[0] += d.BusyMs
		e.retired[1] += d.OnlineMs
		*d = Driver{Num: d.Num}
		return d
	}
	return nil
}

func (e *Engine) goOffline(d *Driver) {
	e.unindexIdle(d)
	e.unindexB2B(d)
	e.setState(d, Offline)
	d.route = nil
	e.emit(Event{Kind: DriverOffline, DriverID: d.ID})
}

func (e *Engine) reopen(t *Trip, d *Driver) {
	t.State = Searching
	t.Driver = nil
	t.B2B = false
	t.declined = append(t.declined, d.ID)
	e.insertOpen(t)
	v := e.view(t)
	e.emit(Event{Kind: TripReopened, Trip: &v, RiderID: t.RiderID, DriverID: d.ID, Reason: "driver_left"})
	if !e.batch {
		e.dispatchOne(t)
	}
}

func (e *Engine) Location(id string, pos geo.Point, heading float64) error {
	d := e.drivers[id]
	if d == nil {
		return ErrUnknownDriver
	}
	if d.State == Offline {
		return ErrBadState
	}
	d.Pos, d.Heading, d.moved = pos, heading, true
	if d.inIdle && (math.Abs(pos.Lat-d.cellPt.Lat) > 2e-4 || math.Abs(pos.Lng-d.cellPt.Lng) > 2e-4) {
		if c := geo.Cell(pos, indexRes); c != d.cell {
			e.unindexIdle(d)
			e.indexIdle(d)
		} else {
			d.cellPt = pos
		}
	}
	if d.route != nil {
		sec, seg, off := d.route.Project(pos, d.routeSeg)
		if off < 60 && sec >= d.routeSec {
			d.routeSec, d.routeSeg = sec, seg
		}
	}
	return nil
}

type RideRequest struct {
	RiderID   string
	Token     string
	Ref       string
	Simulated bool
}

func (e *Engine) Request(r RideRequest) (TripView, error) {
	key := r.RiderID + "\x00" + r.Ref
	if r.Ref != "" {
		if t := e.refs[key]; t != nil {
			return e.view(t), nil
		}
	}
	if e.active[r.RiderID] != nil {
		return TripView{}, ErrRiderBusy
	}
	c, err := e.verify(r.Token, r.RiderID)
	if err != nil {
		return TripView{}, err
	}
	t := &Trip{
		RiderID: r.RiderID, Simulated: r.Simulated, ref: r.Ref, Product: c.P,
		Pickup: named(e.place(c.A), c.U), Dropoff: named(e.place(c.B), c.V), State: Searching,
		Fare: c.F, DriverPay: c.D, Meters: c.M, Seconds: c.S, RequestedAt: e.now,
	}
	t.ID = e.nextID("T", &e.seqTrip)
	t.seq = e.seqTrip
	e.trips[t.ID] = t
	if r.Ref != "" {
		e.refs[key] = t
	}
	e.active[r.RiderID] = t
	e.open = append(e.open, t)
	e.schedule(e.now+int64(e.cfgD.RequestTimeoutS)*1000, tRequestTimeout, t)
	e.stats.request(e, t)
	v := e.view(t)
	e.emit(Event{Kind: TripRequested, Trip: &v, RiderID: t.RiderID})
	if !e.batch {
		e.dispatchOne(t)
	}
	return e.view(t), nil
}

func (e *Engine) Cancel(riderID, tripID string) (TripView, error) {
	t := e.trips[tripID]
	if t == nil {
		return TripView{}, ErrUnknownTrip
	}
	if t.RiderID != riderID {
		return TripView{}, ErrNotYourTrip
	}
	switch t.State {
	case Searching:
		e.removeOpen(t)
		e.cancelTrip(t, "rider", 0)
	case Offering:
		e.withdrawOffer(t.Offer)
		e.cancelTrip(t, "rider", 0)
	case Accepted, Arrived:
		fee := int64(0)
		if e.now-t.AcceptedAt > int64(e.cfgD.CancelGraceS)*1000 {
			fee = int64(math.Round(e.cfgD.CancelFee * 100))
		}
		d := t.Driver
		e.detach(d, t)
		e.cancelTrip(t, "rider", fee)
	default:
		return TripView{}, ErrBadState
	}
	return e.view(t), nil
}

func (e *Engine) detach(d *Driver, t *Trip) {
	if d.Next == t {
		d.Next = nil
		if d.State == OnTrip {
			e.indexB2B(d)
		}
		return
	}
	if d.Trip != t {
		return
	}
	d.Trip, d.route = nil, nil
	if d.GoOffline {
		e.goOffline(d)
		return
	}
	e.setState(d, Idle)
	e.indexIdle(d)
}

func (e *Engine) cancelTrip(t *Trip, reason string, fee int64) {
	t.State = Cancelled
	t.CancelReason = reason
	t.CancelFee = fee
	t.EndedAt = e.now
	if fee > 0 && t.Driver != nil {
		driverShare := fee * 8 / 10
		e.post("cancel_fee", t, []Line{
			{"rider:" + t.RiderID, -fee},
			{"driver:" + t.Driver.ID, driverShare},
			{"platform", fee - driverShare},
		})
		t.Driver.Earnings += driverShare
	}
	e.stats.cancelled(e, t)
	v := e.view(t)
	e.emit(Event{Kind: TripCancelled, Trip: &v, RiderID: t.RiderID, Reason: reason})
	e.finish(t)
}

func (e *Engine) finish(t *Trip) {
	if e.active[t.RiderID] == t {
		delete(e.active, t.RiderID)
	}
	e.finished = append(e.finished, t)
}

func (e *Engine) evict() {
	cut := 0
	for cut < len(e.finished) && e.now-e.finished[cut].EndedAt > keepFinished {
		t := e.finished[cut]
		delete(e.trips, t.ID)
		if t.ref != "" {
			delete(e.refs, t.RiderID+"\x00"+t.ref)
		}
		cut++
	}
	e.finished = append(e.finished[:0], e.finished[cut:]...)
}

func (e *Engine) driverFor(driverID, tripID string) (*Driver, *Trip, error) {
	d := e.drivers[driverID]
	if d == nil {
		return nil, nil, ErrUnknownDriver
	}
	t := e.trips[tripID]
	if t == nil {
		return nil, nil, ErrUnknownTrip
	}
	if d.Trip != t {
		return nil, nil, ErrBadState
	}
	return d, t, nil
}

func (e *Engine) Accept(driverID, offerID string) error {
	d := e.drivers[driverID]
	if d == nil {
		return ErrUnknownDriver
	}
	o := d.Offer
	if o == nil || o.ID != offerID {
		return ErrNoOffer
	}
	if e.now > o.ExpiresAt {
		return ErrOfferExpired
	}
	t := o.Trip
	d.Offer, t.Offer = nil, nil
	e.emit(Event{Kind: OfferClosed, Offer: e.offerView(o), DriverID: d.ID, Reason: "accepted"})
	t.State = Accepted
	t.Driver = d
	t.AcceptedAt = e.now
	t.PickupETA = o.ETA
	t.B2B = o.B2B
	e.stats.accepted(e, t)
	if o.B2B {
		d.Next = t
		v := e.view(t)
		e.emit(Event{Kind: TripAccepted, Trip: &v, RiderID: t.RiderID, DriverID: d.ID})
		return nil
	}
	d.Trip = t
	e.setState(d, EnRoute)
	v := e.view(t)
	e.emit(Event{Kind: TripAccepted, Trip: &v, RiderID: t.RiderID, DriverID: d.ID})
	e.routeTo(d, t.Pickup.Node, "pickup")
	return nil
}

func (e *Engine) Decline(driverID, offerID string) error {
	d := e.drivers[driverID]
	if d == nil {
		return ErrUnknownDriver
	}
	o := d.Offer
	if o == nil || o.ID != offerID {
		return ErrNoOffer
	}
	e.stats.Declined++
	e.closeOffer(o, "declined")
	return nil
}

func (e *Engine) closeOffer(o *Offer, reason string) {
	t, d := o.Trip, o.Driver
	d.Offer, t.Offer = nil, nil
	t.declined = append(t.declined, d.ID)
	switch {
	case o.B2B && d.State == OnTrip:
		e.indexB2B(d)
	case d.State == Offered:
		e.setState(d, Idle)
		e.indexIdle(d)
	}
	e.emit(Event{Kind: OfferClosed, Offer: e.offerView(o), DriverID: d.ID, Reason: reason})
	if t.State == Offering {
		t.State = Searching
		e.insertOpen(t)
		if !e.batch {
			e.dispatchOne(t)
		}
	}
}

func (e *Engine) withdrawOffer(o *Offer) {
	t, d := o.Trip, o.Driver
	d.Offer, t.Offer = nil, nil
	switch {
	case o.B2B && d.State == OnTrip:
		e.indexB2B(d)
	case d.State == Offered:
		e.setState(d, Idle)
		e.indexIdle(d)
	}
	e.emit(Event{Kind: OfferClosed, Offer: e.offerView(o), DriverID: d.ID, Reason: "cancelled"})
}

func (e *Engine) Arrive(driverID, tripID string) error {
	d, t, err := e.driverFor(driverID, tripID)
	if err != nil {
		return err
	}
	if t.State != Accepted || d.State != EnRoute {
		return ErrBadState
	}
	if geo.Distance(d.Pos, t.Pickup.Pt) > e.cfgD.ArriveRadiusM {
		return ErrNotHere
	}
	t.State = Arrived
	t.ArrivedAt = e.now
	e.setState(d, AtPickup)
	d.route = nil
	e.stats.arrived(e, t)
	v := e.view(t)
	e.emit(Event{Kind: DriverArrived, Trip: &v, RiderID: t.RiderID, DriverID: d.ID})
	return nil
}

func (e *Engine) StartTrip(driverID, tripID string) error {
	d, t, err := e.driverFor(driverID, tripID)
	if err != nil {
		return err
	}
	if t.State != Arrived || d.State != AtPickup {
		return ErrBadState
	}
	t.State = Riding
	t.StartedAt = e.now
	e.setState(d, OnTrip)
	v := e.view(t)
	e.emit(Event{Kind: TripStarted, Trip: &v, RiderID: t.RiderID, DriverID: d.ID})
	e.routeTo(d, t.Dropoff.Node, "dropoff")
	e.indexB2B(d)
	return nil
}

func (e *Engine) Complete(driverID, tripID string) error {
	d, t, err := e.driverFor(driverID, tripID)
	if err != nil {
		return err
	}
	if t.State != Riding || d.State != OnTrip {
		return ErrBadState
	}
	if geo.Distance(d.Pos, t.Dropoff.Pt) > e.cfgD.ArriveRadiusM {
		return ErrNotHere
	}
	t.State = Completed
	t.EndedAt = e.now
	e.unindexB2B(d)
	f := t.Fare
	e.post("fare", t, []Line{
		{"rider:" + t.RiderID, -f.Total},
		{"driver:" + d.ID, t.DriverPay},
		{"platform", f.Subtotal - t.DriverPay},
		{"tax:sales", f.SalesTax},
		{"tax:black_car_fund", f.BlackCarFund},
		{"fee:congestion", f.Congestion},
		{"fee:cbd", f.CBD},
	})
	d.Trips++
	d.Earnings += t.DriverPay
	e.stats.completed(e, t)
	v := e.view(t)
	e.emit(Event{Kind: TripCompleted, Trip: &v, RiderID: t.RiderID, DriverID: d.ID})
	e.finish(t)
	d.Trip, d.route = nil, nil
	switch {
	case d.Next != nil:
		n := d.Next
		d.Next = nil
		d.Trip = n
		e.setState(d, EnRoute)
		e.routeTo(d, n.Pickup.Node, "pickup")
	case d.Offer != nil:
		d.Offer.B2B = false
		e.setState(d, Offered)
	case d.GoOffline:
		e.goOffline(d)
	default:
		e.setState(d, Idle)
		e.indexIdle(d)
	}
	return nil
}

func (e *Engine) Rate(riderID, tripID string, stars int) error {
	t := e.trips[tripID]
	if t == nil {
		return ErrUnknownTrip
	}
	if t.RiderID != riderID {
		return ErrNotYourTrip
	}
	if t.State != Completed {
		return ErrBadState
	}
	if stars < 1 || stars > 5 {
		return ErrBadRating
	}
	if t.Rating != 0 {
		return ErrAlreadyRated
	}
	t.Rating = stars
	if d := t.Driver; d != nil {
		d.Rating = math.Round((d.Rating*0.95+float64(stars)*0.05)*100) / 100
	}
	v := e.view(t)
	e.emit(Event{Kind: TripRated, Trip: &v, RiderID: riderID})
	return nil
}

func (e *Engine) post(kind string, t *Trip, lines []Line) {
	var sum int64
	for _, l := range lines {
		sum += l.Cents
	}
	if sum != 0 {
		panic(fmt.Sprintf("journal for %s does not balance: %d", t.ID, sum))
	}
	j := &Journal{ID: e.nextID("J", &e.seqJournal), TripID: t.ID, At: e.now, Kind: kind, Lines: lines}
	for _, l := range lines {
		e.books[book(l.Account)] += l.Cents
	}
	e.emit(Event{Kind: Posted, Journal: j})
}

func book(account string) string {
	for i := 0; i < len(account); i++ {
		if account[i] == ':' && (account[:i] == "rider" || account[:i] == "driver") {
			return account[:i]
		}
	}
	return account
}

func (e *Engine) Books() map[string]int64 {
	out := make(map[string]int64, len(e.books))
	for k, v := range e.books {
		out[k] = v
	}
	return out
}

func (e *Engine) OpenCount() int { return len(e.open) }

func (e *Engine) Count(s DriverState) int { return e.counts[s] }
