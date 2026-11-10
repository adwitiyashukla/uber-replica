package engine

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/adwitiyashukla/uber-replica/internal/city"
	"github.com/adwitiyashukla/uber-replica/internal/config"
	"github.com/adwitiyashukla/uber-replica/internal/geo"
)

var (
	cityOnce sync.Once
	testCity *city.City
	cityErr  error
)

const sixPM = 18 * 3600 * 1000

func loadCity(t testing.TB) *city.City {
	cityOnce.Do(func() { testCity, cityErr = city.Load("../../data") })
	if cityErr != nil {
		t.Fatalf("load city: %v", cityErr)
	}
	return testCity
}

func newEngine(t testing.TB, edit func(*config.Config)) *Engine {
	cfg := config.Default()
	if edit != nil {
		edit(&cfg)
	}
	e := New(cfg, loadCity(t), []byte("test-secret"))
	e.Start(sixPM)
	return e
}

func node(e *Engine, lat, lng float64) geo.Point {
	return e.g.Point(e.g.Nearest(geo.Point{Lat: lat, Lng: lng}))
}

var (
	timesSquare  = geo.Point{Lat: 40.7580, Lng: -73.9855}
	grandCentral = geo.Point{Lat: 40.7527, Lng: -73.9772}
	unionSquare  = geo.Point{Lat: 40.7359, Lng: -73.9911}
	harlem       = geo.Point{Lat: 40.8116, Lng: -73.9465}
	bryantPark   = geo.Point{Lat: 40.7536, Lng: -73.9832}
	penn         = geo.Point{Lat: 40.7506, Lng: -73.9935}
)

func online(t testing.TB, e *Engine, id string, p geo.Point, prod Product) {
	if _, err := e.DriverOnline(DriverInfo{ID: id, Name: id, Product: prod, Rating: 4.9}, node(e, p.Lat, p.Lng)); err != nil {
		t.Fatalf("online %s: %v", id, err)
	}
}

func request(t testing.TB, e *Engine, rider string, from, to geo.Point, prod Product) TripView {
	q, err := e.Quote(rider, from, to)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	v, err := e.Request(RideRequest{RiderID: rider, Token: q.Options[prod].Token, Ref: rider + "-1"})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return v
}

func step(e *Engine, ms int64) []Event {
	e.Advance(e.now + ms)
	return e.Drain()
}

func find(evs []Event, k Kind) *Event {
	for i := range evs {
		if evs[i].Kind == k {
			return &evs[i]
		}
	}
	return nil
}

func driveToEnd(t testing.TB, e *Engine, id string) {
	tr, _, ok := e.DriverRoute(id)
	if !ok {
		t.Fatalf("driver %s has no route", id)
	}
	p, h := tr.At(tr.Duration())
	if err := e.Location(id, p, h); err != nil {
		t.Fatalf("location: %v", err)
	}
}

func TestHungarianMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for trial := 0; trial < 400; trial++ {
		n, m := 1+rng.Intn(5), 1+rng.Intn(6)
		a := make([][]float64, n)
		for i := range a {
			a[i] = make([]float64, m)
			for j := range a[i] {
				a[i][j] = float64(rng.Intn(100))
				if rng.Float64() < 0.2 {
					a[i][j] = unmatched
				}
			}
		}
		got := Hungarian(a)
		used := map[int]bool{}
		total := 0.0
		for i, j := range got {
			if j < 0 {
				continue
			}
			if used[j] {
				t.Fatalf("column %d used twice", j)
			}
			used[j] = true
			total += a[i][j]
		}
		if want := bruteAssign(a); math.Abs(total-want) > 1e-6 {
			t.Fatalf("trial %d: hungarian %v brute %v for %v", trial, total, want, a)
		}
	}
}

func bruteAssign(a [][]float64) float64 {
	n, m := len(a), len(a[0])
	k := min(n, m)
	best := math.Inf(1)
	var rec func(i int, used []bool, rowsLeft int, sum float64)
	rec = func(i int, used []bool, assigned int, sum float64) {
		if i == n {
			if assigned == k && sum < best {
				best = sum
			}
			return
		}
		if n-i > k-assigned {
			rec(i+1, used, assigned, sum)
		}
		for j := 0; j < m; j++ {
			if !used[j] {
				used[j] = true
				rec(i+1, used, assigned+1, sum+a[i][j])
				used[j] = false
			}
		}
	}
	rec(0, make([]bool, m), 0, 0)
	return best
}

func TestFullTripLifecycle(t *testing.T) {
	e := newEngine(t, nil)
	online(t, e, "D1", grandCentral, Standard)
	trip := request(t, e, "R1", timesSquare, unionSquare, Standard)
	if trip.State != Searching || trip.Fare.Total <= trip.Fare.Subtotal {
		t.Fatalf("unexpected new trip %+v", trip)
	}
	evs := step(e, 2000)
	sent := find(evs, OfferSent)
	if sent == nil || sent.DriverID != "D1" {
		t.Fatalf("expected an offer to D1, got %v", evs)
	}
	if err := e.Accept("D1", sent.Offer.ID); err != nil {
		t.Fatal(err)
	}
	evs = e.Drain()
	if find(evs, TripAccepted) == nil || find(evs, DriverRouted) == nil {
		t.Fatalf("missing accept or route events: %v", evs)
	}
	if err := e.Arrive("D1", trip.ID); !errors.Is(err, ErrNotHere) {
		t.Fatalf("arriving from far away should fail, got %v", err)
	}
	driveToEnd(t, e, "D1")
	if v, _ := e.Trip(trip.ID); v.ETA > 1 {
		t.Fatalf("eta should be zero at the pickup, got %v", v.ETA)
	}
	if err := e.Arrive("D1", trip.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.StartTrip("D1", trip.ID); err != nil {
		t.Fatal(err)
	}
	driveToEnd(t, e, "D1")
	if err := e.Complete("D1", trip.ID); err != nil {
		t.Fatal(err)
	}
	v, _ := e.Trip(trip.ID)
	if v.State != Completed {
		t.Fatalf("state %v", v.State)
	}
	d, _ := e.Driver("D1")
	if d.State != Idle || d.Earnings != v.DriverPay || d.Trips != 1 {
		t.Fatalf("driver after trip %+v", d)
	}
	assertBalanced(t, e)
	if e.Books()["rider"] != -v.Fare.Total {
		t.Fatalf("rider book %v want %v", e.Books()["rider"], -v.Fare.Total)
	}
	if err := e.Rate("R1", trip.ID, 5); err != nil {
		t.Fatal(err)
	}
	if err := e.Rate("R1", trip.ID, 4); !errors.Is(err, ErrAlreadyRated) {
		t.Fatalf("second rating should fail, got %v", err)
	}
}

func assertBalanced(t testing.TB, e *Engine) {
	var sum int64
	for _, v := range e.books {
		sum += v
	}
	if sum != 0 {
		t.Fatalf("books do not balance: %v", e.books)
	}
}

func TestQuoteTokens(t *testing.T) {
	e := newEngine(t, nil)
	q, err := e.Quote("R1", timesSquare, harlem)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Options) != 3 || q.Options[XL].Fare.Total <= q.Options[Standard].Fare.Total {
		t.Fatalf("xl should cost more than standard: %+v", q.Options)
	}
	tok := q.Options[Standard].Token
	tampered := strings.Replace(tok, tok[5:6], string(rune(tok[5]^1)), 1)
	if _, err := e.Request(RideRequest{RiderID: "R1", Token: tampered}); !errors.Is(err, ErrBadQuote) {
		t.Fatalf("tampered token accepted: %v", err)
	}
	if _, err := e.Request(RideRequest{RiderID: "R2", Token: tok}); !errors.Is(err, ErrBadQuote) {
		t.Fatalf("token used by another rider: %v", err)
	}
	e.Advance(e.now + int64(e.cfgP.QuoteTTLS+1)*1000)
	if _, err := e.Request(RideRequest{RiderID: "R1", Token: tok}); !errors.Is(err, ErrQuoteExpired) {
		t.Fatalf("expired token accepted: %v", err)
	}
	if _, err := e.Quote("R1", geo.Point{Lat: 40.70, Lng: -74.10}, harlem); !errors.Is(err, ErrOutsideService) {
		t.Fatalf("New Jersey pickup should be rejected: %v", err)
	}
}

func TestRequestIsIdempotent(t *testing.T) {
	e := newEngine(t, nil)
	q, _ := e.Quote("R1", timesSquare, unionSquare)
	a, err := e.Request(RideRequest{RiderID: "R1", Token: q.Options[0].Token, Ref: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Request(RideRequest{RiderID: "R1", Token: q.Options[0].Token, Ref: "abc"})
	if err != nil || a.ID != b.ID {
		t.Fatalf("retry with the same ref should return the same trip: %v %v %v", a.ID, b.ID, err)
	}
	if _, err := e.Request(RideRequest{RiderID: "R1", Token: q.Options[0].Token, Ref: "xyz"}); !errors.Is(err, ErrRiderBusy) {
		t.Fatalf("second active trip should be refused: %v", err)
	}
}

func TestPricing(t *testing.T) {
	e := newEngine(t, nil)
	cal := e.city.Cal
	f, pay := e.Price(Standard, 100, 30, nil, 1)
	if f.Base != cents(cal.Fare.Min) {
		t.Fatalf("short trip should hit the minimum fare: %+v", f)
	}
	if f.Congestion != 0 || f.CBD != 0 || pay <= 0 {
		t.Fatalf("no zones means no zone fees: %+v %v", f, pay)
	}
	midtown := e.g.Zone[e.g.Nearest(timesSquare)]
	f, _ = e.Price(Standard, 5000, 1200, []uint16{midtown}, 1.5)
	if f.Congestion != 275 || f.CBD != 150 {
		t.Fatalf("midtown trip should pay both zone fees: %+v", f)
	}
	if f.Total != f.Subtotal+f.SalesTax+f.BlackCarFund+f.Congestion+f.CBD || f.SurgeAmount != f.Subtotal-f.Base {
		t.Fatalf("fare parts do not add up: %+v", f)
	}
	if math.Abs(float64(f.Subtotal)/float64(f.Base)-1.5) > 0.01 {
		t.Fatalf("surge not applied: %+v", f)
	}
}

func TestDeclineMovesOfferToNextDriver(t *testing.T) {
	e := newEngine(t, nil)
	online(t, e, "D1", grandCentral, Standard)
	online(t, e, "D2", bryantPark, Standard)
	trip := request(t, e, "R1", timesSquare, harlem, Standard)
	first := find(step(e, 2000), OfferSent)
	if first == nil {
		t.Fatal("no offer")
	}
	if err := e.Decline(first.DriverID, first.Offer.ID); err != nil {
		t.Fatal(err)
	}
	second := find(step(e, 2000), OfferSent)
	if second == nil || second.DriverID == first.DriverID {
		t.Fatalf("expected the other driver to get the offer, got %+v", second)
	}
	if v, _ := e.Trip(trip.ID); v.Offers != 2 || v.State != Offering {
		t.Fatalf("trip after decline %+v", v)
	}
}

func TestOfferExpiresAndRequestTimesOut(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.Dispatch.RequestTimeoutS = 40 })
	online(t, e, "D1", grandCentral, Standard)
	trip := request(t, e, "R1", timesSquare, unionSquare, Standard)
	if find(step(e, 2000), OfferSent) == nil {
		t.Fatal("no offer")
	}
	evs := step(e, 16000)
	closed := find(evs, OfferClosed)
	if closed == nil || closed.Reason != "expired" {
		t.Fatalf("offer should expire, got %v", evs)
	}
	if d, _ := e.Driver("D1"); d.State != Idle {
		t.Fatalf("driver should be idle again, is %v", d.State)
	}
	evs = step(e, 30000)
	c := find(evs, TripCancelled)
	if c == nil || c.Reason != "no_drivers" {
		t.Fatalf("request should time out, got %v", evs)
	}
	if v, _ := e.Trip(trip.ID); v.State != Cancelled {
		t.Fatalf("trip state %v", v.State)
	}
}

func TestCancelFeeAfterGrace(t *testing.T) {
	for _, late := range []bool{false, true} {
		e := newEngine(t, nil)
		online(t, e, "D1", penn, Standard)
		trip := request(t, e, "R1", timesSquare, unionSquare, Standard)
		o := find(step(e, 2000), OfferSent)
		if o == nil {
			t.Fatal("no offer")
		}
		if err := e.Accept("D1", o.Offer.ID); err != nil {
			t.Fatal(err)
		}
		if late {
			step(e, int64(e.cfgD.CancelGraceS+5)*1000)
		}
		v, err := e.Cancel("R1", trip.ID)
		if err != nil {
			t.Fatal(err)
		}
		if late != (v.CancelFee > 0) {
			t.Fatalf("late=%v fee=%v", late, v.CancelFee)
		}
		if d, _ := e.Driver("D1"); d.State != Idle {
			t.Fatalf("driver should be released, is %v", d.State)
		}
		assertBalanced(t, e)
	}
}

func TestBackToBackOffer(t *testing.T) {
	e := newEngine(t, nil)
	online(t, e, "D1", unionSquare, Standard)
	first := request(t, e, "R1", unionSquare, grandCentral, Standard)
	o := find(step(e, 2000), OfferSent)
	_ = e.Accept("D1", o.Offer.ID)
	driveToEnd(t, e, "D1")
	_ = e.Arrive("D1", first.ID)
	_ = e.StartTrip("D1", first.ID)
	tr, _, _ := e.DriverRoute("D1")
	p, h := tr.At(tr.Duration() - 60)
	_ = e.Location("D1", p, h)
	second := request(t, e, "R2", node(e, 40.7540, -73.9760), harlem, Standard)
	evs := step(e, 2000)
	o2 := find(evs, OfferSent)
	if o2 == nil || !o2.Offer.B2B || o2.DriverID != "D1" {
		t.Fatalf("expected a back-to-back offer to D1, got %v", evs)
	}
	_ = e.Accept("D1", o2.Offer.ID)
	if d, _ := e.Driver("D1"); d.Next == nil || d.Next.ID != second.ID || d.State != OnTrip {
		t.Fatalf("driver should keep riding with the next trip queued: %+v", d)
	}
	driveToEnd(t, e, "D1")
	if err := e.Complete("D1", first.ID); err != nil {
		t.Fatal(err)
	}
	d, _ := e.Driver("D1")
	if d.State != EnRoute || d.Trip == nil || d.Trip.ID != second.ID {
		t.Fatalf("driver should head to the next pickup: %+v", d)
	}
}

func TestQuoteCountsCarsFinishingLater(t *testing.T) {
	e := newEngine(t, nil)
	online(t, e, "D1", unionSquare, Standard)
	first := request(t, e, "R1", unionSquare, harlem, Standard)
	o := find(step(e, 2000), OfferSent)
	_ = e.Accept("D1", o.Offer.ID)
	driveToEnd(t, e, "D1")
	_ = e.Arrive("D1", first.ID)
	_ = e.StartTrip("D1", first.ID)
	q, err := e.Quote("R2", node(e, 40.8100, -73.9500), unionSquare)
	if err != nil {
		t.Fatal(err)
	}
	if q.Options[Standard].PickupETA >= 0 {
		t.Fatalf("no car is free or close to free yet, got %v", q.Options[Standard].PickupETA)
	}
	e.FillETAs(&q)
	left := e.remaining(e.drivers["D1"])
	if eta := q.Options[Standard].PickupETA; eta < left {
		t.Fatalf("estimate %v should include the %v seconds left on the current trip", eta, left)
	}
	if q.Options[XL].PickupETA >= 0 {
		t.Fatalf("a standard car cannot take an XL ride, got %v", q.Options[XL].PickupETA)
	}
}

func TestDriverLeavingReopensTrip(t *testing.T) {
	e := newEngine(t, nil)
	online(t, e, "D1", grandCentral, Standard)
	trip := request(t, e, "R1", timesSquare, harlem, Standard)
	o := find(step(e, 2000), OfferSent)
	_ = e.Accept("D1", o.Offer.ID)
	e.Drain()
	if err := e.DriverOffline("D1"); err != nil {
		t.Fatal(err)
	}
	evs := e.Drain()
	if find(evs, TripReopened) == nil || find(evs, DriverOffline) == nil {
		t.Fatalf("expected reopen and offline events: %v", evs)
	}
	if v, _ := e.Trip(trip.ID); v.State != Searching || v.Driver != nil {
		t.Fatalf("trip should be searching again: %+v", v)
	}
	online(t, e, "D2", bryantPark, Standard)
	if o := find(step(e, 2000), OfferSent); o == nil || o.DriverID != "D2" {
		t.Fatalf("new driver should get the reopened trip")
	}
}

func TestComfortTripSkipsStandardCars(t *testing.T) {
	e := newEngine(t, nil)
	online(t, e, "D1", timesSquare, Standard)
	online(t, e, "D2", penn, Comfort)
	request(t, e, "R1", timesSquare, unionSquare, Comfort)
	if o := find(step(e, 2000), OfferSent); o == nil || o.DriverID != "D2" {
		t.Fatalf("comfort trip went to %v", o)
	}
}

func TestBatchBeatsGreedyOnAConflict(t *testing.T) {
	total := func(policy string) float64 {
		e := newEngine(t, func(c *config.Config) { c.Dispatch.Policy = policy })
		online(t, e, "A", node(e, 40.7505, -73.9934), Standard)
		online(t, e, "B", node(e, 40.7480, -73.9860), Standard)
		q1, _ := e.Quote("R1", node(e, 40.7491, -73.9900), harlem)
		q2, _ := e.Quote("R2", node(e, 40.7512, -73.9945), harlem)
		_, _ = e.Request(RideRequest{RiderID: "R1", Token: q1.Options[0].Token})
		_, _ = e.Request(RideRequest{RiderID: "R2", Token: q2.Options[0].Token})
		sum := 0.0
		for _, ev := range step(e, 2000) {
			if ev.Kind == OfferSent {
				sum += ev.Offer.ETA
			}
		}
		for _, ev := range e.Drain() {
			if ev.Kind == OfferSent {
				sum += ev.Offer.ETA
			}
		}
		return sum
	}
	b, g := total("batch"), total("greedy")
	if b > g*1.001 {
		t.Fatalf("batch total eta %v should not exceed greedy %v", b, g)
	}
}

func TestSurgeRisesAndFalls(t *testing.T) {
	e := newEngine(t, nil)
	for i := 0; i < 40; i++ {
		_, _ = e.Quote(fmt.Sprintf("R%d", i), timesSquare, harlem)
		step(e, 3000)
	}
	if s := e.SurgeAt(timesSquare); s <= 1.5 {
		t.Fatalf("surge should rise with demand and no cars, got %v", s)
	}
	for i := 0; i < 60; i++ {
		online(t, e, fmt.Sprintf("D%d", i), node(e, 40.758+float64(i%8)*0.001, -73.9855+float64(i/8)*0.001), Standard)
	}
	for i := 0; i < 120; i++ {
		step(e, 10000)
	}
	if s := e.SurgeAt(timesSquare); s != 1 {
		t.Fatalf("surge should settle back to 1 with plenty of cars, got %v", s)
	}
}

type chaos struct {
	t      *testing.T
	e      *Engine
	rng    *rand.Rand
	nodes  []geo.Point
	events []Event
}

func (c *chaos) point() geo.Point { return c.nodes[c.rng.Intn(len(c.nodes))] }

func (c *chaos) run(steps int) {
	e := c.e
	for i := 0; i < 60; i++ {
		_, _ = e.DriverOnline(DriverInfo{ID: fmt.Sprintf("D%02d", i), Product: Product(i % 3), Rating: 4.8}, c.point())
	}
	for s := 0; s < steps; s++ {
		e.Advance(e.now + int64(c.rng.Intn(3000)))
		switch r := c.rng.Intn(100); {
		case r < 25:
			rider := fmt.Sprintf("R%03d", c.rng.Intn(150))
			if q, err := e.Quote(rider, c.point(), c.point()); err == nil {
				_, _ = e.Request(RideRequest{RiderID: rider, Token: q.Options[c.rng.Intn(3)].Token, Ref: fmt.Sprint(s)})
			}
		case r < 45:
			for _, d := range e.byNum {
				if d.Offer != nil && c.rng.Intn(3) == 0 {
					if c.rng.Intn(5) == 0 {
						_ = e.Decline(d.ID, d.Offer.ID)
					} else {
						_ = e.Accept(d.ID, d.Offer.ID)
					}
				}
			}
		case r < 75:
			for _, d := range e.byNum {
				if d.route == nil || d.Trip == nil || c.rng.Intn(2) == 0 {
					continue
				}
				p, h := d.route.At(d.route.Duration())
				_ = e.Location(d.ID, p, h)
				switch d.Trip.State {
				case Accepted:
					_ = e.Arrive(d.ID, d.Trip.ID)
				case Riding:
					_ = e.Complete(d.ID, d.Trip.ID)
				}
			}
			for _, d := range e.byNum {
				if d.State == AtPickup && c.rng.Intn(2) == 0 {
					_ = e.StartTrip(d.ID, d.Trip.ID)
				}
			}
		case r < 82:
			ids := make([]string, 0, len(e.trips))
			for id := range e.trips {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			if len(ids) > 0 {
				t := e.trips[ids[c.rng.Intn(len(ids))]]
				_, _ = e.Cancel(t.RiderID, t.ID)
			}
		case r < 88:
			d := e.byNum[c.rng.Intn(len(e.byNum))]
			if d.State == Offline {
				_, _ = e.DriverOnline(d.DriverInfo, c.point())
			} else {
				_ = e.DriverOffline(d.ID)
			}
		default:
			d := e.byNum[c.rng.Intn(len(e.byNum))]
			if d.State == Idle {
				_ = e.Location(d.ID, c.point(), 0)
			}
		}
		c.events = append(c.events, e.Drain()...)
		c.check(s)
	}
}

func (c *chaos) check(step int) {
	e := c.e
	fail := func(format string, args ...any) {
		c.t.Fatalf("step %d: %s", step, fmt.Sprintf(format, args...))
	}
	var counts [6]int
	holders := map[*Trip]*Driver{}
	for _, d := range e.byNum {
		counts[d.State]++
		if d.inIdle != (d.State == Idle) {
			fail("driver %s idle index flag %v in state %v", d.ID, d.inIdle, d.State)
		}
		if (d.State == Offered) != (d.Offer != nil && !d.Offer.B2B) {
			fail("driver %s state %v but offer %+v", d.ID, d.State, d.Offer)
		}
		if d.Offer != nil && (d.Offer.Trip.Offer != d.Offer || d.Offer.Trip.State != Offering) {
			fail("offer %s is not the trip's current offer", d.Offer.ID)
		}
		for _, t := range []*Trip{d.Trip, d.Next} {
			if t == nil {
				continue
			}
			if other := holders[t]; other != nil {
				fail("trip %s held by %s and %s", t.ID, other.ID, d.ID)
			}
			holders[t] = d
			if t.Driver != d {
				fail("trip %s driver pointer mismatch", t.ID)
			}
		}
		if d.State.Busy() && d.Trip == nil {
			fail("busy driver %s without a trip", d.ID)
		}
	}
	if counts != e.counts {
		fail("state counts %v but tracked %v", counts, e.counts)
	}
	idle := 0
	for cell, ds := range e.idle {
		for i, d := range ds {
			if d.cell != cell || d.cellPos != i {
				fail("idle index corrupt for %s", d.ID)
			}
			idle++
		}
	}
	if idle != counts[Idle] {
		fail("idle index has %d drivers, %d are idle", idle, counts[Idle])
	}
	for i, t := range e.open {
		if t.State != Searching {
			fail("open list has trip %s in state %v", t.ID, t.State)
		}
		if i > 0 && e.open[i-1].seq >= t.seq {
			fail("open list out of order")
		}
	}
	searching := 0
	for _, t := range e.trips {
		if t.State == Searching {
			searching++
		}
		if (t.State == Accepted || t.State == Arrived || t.State == Riding) && holders[t] == nil {
			fail("trip %s in %v has no driver holding it", t.ID, t.State)
		}
	}
	if searching != len(e.open) {
		fail("%d searching trips but open list has %d", searching, len(e.open))
	}
	var sum int64
	for _, v := range e.books {
		sum += v
	}
	if sum != 0 {
		fail("books do not balance")
	}
}

func newChaos(t *testing.T, seed int64, policy string) *chaos {
	e := newEngine(t, func(c *config.Config) { c.Dispatch.Policy = policy })
	rng := rand.New(rand.NewSource(seed))
	var nodes []geo.Point
	for len(nodes) < 300 {
		nodes = append(nodes, e.g.Point(uint32(rng.Intn(e.g.N()))))
	}
	return &chaos{t: t, e: e, rng: rng, nodes: nodes}
}

func TestInvariantsUnderRandomCommands(t *testing.T) {
	for _, policy := range []string{"batch", "greedy"} {
		for seed := int64(1); seed <= 4; seed++ {
			c := newChaos(t, seed, policy)
			c.run(1500)
			if len(c.events) == 0 {
				t.Fatal("no events")
			}
		}
	}
}

func TestDeterministicReplay(t *testing.T) {
	a := newChaos(t, 42, "batch")
	a.run(800)
	b := newChaos(t, 42, "batch")
	b.run(800)
	if fa, fb := Fingerprint(a.events), Fingerprint(b.events); fa != fb || len(a.events) != len(b.events) {
		t.Fatalf("same seed gave different event logs: %x vs %x (%d vs %d events)", fa, fb, len(a.events), len(b.events))
	}
	c := newChaos(t, 43, "batch")
	c.run(800)
	if Fingerprint(c.events) == Fingerprint(a.events) {
		t.Fatal("different seeds should give different logs")
	}
}

func BenchmarkLocationUpdate(b *testing.B) {
	e := newEngine(b, nil)
	rng := rand.New(rand.NewSource(1))
	var pts []geo.Point
	for i := 0; i < 5000; i++ {
		p := e.g.Point(uint32(rng.Intn(e.g.N())))
		pts = append(pts, p)
		_, _ = e.DriverOnline(DriverInfo{ID: fmt.Sprintf("D%d", i), Simulated: true}, p)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d := e.byNum[i%5000]
		_ = e.Location(d.ID, pts[(i*7+i/5000)%5000], 0)
	}
}

func BenchmarkHungarian(b *testing.B) {
	for _, n := range []int{10, 50, 100, 200} {
		b.Run(fmt.Sprintf("trips=%d,drivers=%d", n, 4*n), func(b *testing.B) {
			rng := rand.New(rand.NewSource(1))
			a := make([][]float64, n)
			for i := range a {
				a[i] = make([]float64, 4*n)
				for j := range a[i] {
					a[i][j] = rng.Float64() * 900
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				Hungarian(a)
			}
		})
	}
}

func BenchmarkCandidateSearch(b *testing.B) {
	e := newEngine(b, nil)
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 3000; i++ {
		_, _ = e.DriverOnline(DriverInfo{ID: fmt.Sprintf("D%d", i), Simulated: true}, e.g.Point(uint32(rng.Intn(e.g.N()))))
	}
	trips := make([]*Trip, 200)
	for i := range trips {
		n := uint32(rng.Intn(e.g.N()))
		trips[i] = &Trip{Pickup: e.place(n)}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.candidates(trips[i%len(trips)])
	}
}

func BenchmarkNearestLinearScan(b *testing.B) {
	e := newEngine(b, nil)
	rng := rand.New(rand.NewSource(2))
	var ds []*Driver
	for i := 0; i < 3000; i++ {
		v, _ := e.DriverOnline(DriverInfo{ID: fmt.Sprintf("D%d", i), Simulated: true}, e.g.Point(uint32(rng.Intn(e.g.N()))))
		ds = append(ds, e.drivers[v.ID])
	}
	targets := make([]geo.Point, 200)
	for i := range targets {
		targets[i] = e.g.Point(uint32(rng.Intn(e.g.N())))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := targets[i%len(targets)]
		best := make([]float64, 0, len(ds))
		for _, d := range ds {
			best = append(best, geo.Distance(p, d.Pos))
		}
		sort.Float64s(best)
	}
}

func TestVisitorsGetAHeadStart(t *testing.T) {
	winner := func(headStart float64) (string, float64) {
		e := newEngine(t, func(c *config.Config) { c.Dispatch.VisitorHeadStartS = headStart })
		info := DriverInfo{ID: "S1", Name: "S1", Product: Standard, Rating: 4.9, Simulated: true}
		if _, err := e.DriverOnline(info, node(e, timesSquare.Lat, timesSquare.Lng)); err != nil {
			t.Fatal(err)
		}
		online(t, e, "H1", node(e, 40.7590, -73.9850), Standard)
		request(t, e, "R1", timesSquare, harlem, Standard)
		o := find(step(e, 2000), OfferSent)
		if o == nil {
			t.Fatal("no offer sent")
		}
		return o.DriverID, o.Offer.ETA
	}
	if w, _ := winner(0); w != "S1" {
		t.Fatalf("without a head start the closer simulated car should win, got %s", w)
	}
	if w, eta := winner(120); w != "H1" || eta <= 0 {
		t.Fatalf("with a head start the visitor should win with a real eta, got %s %v", w, eta)
	}
}

func TestLongOfflineDriversAreRecycled(t *testing.T) {
	e := newEngine(t, nil)
	online(t, e, "H1", timesSquare, Standard)
	if err := e.DriverOffline("H1"); err != nil {
		t.Fatal(err)
	}
	step(e, 30*60*1000)
	online(t, e, "H2", timesSquare, Standard)
	if len(e.byNum) != 2 {
		t.Fatalf("a driver offline for half an hour should keep its slot, have %d slots", len(e.byNum))
	}
	if err := e.DriverOffline("H2"); err != nil {
		t.Fatal(err)
	}
	step(e, 31*60*1000)
	online(t, e, "H3", timesSquare, Standard)
	if len(e.byNum) != 2 {
		t.Fatalf("H3 should reuse the slot H1 left an hour ago, have %d slots", len(e.byNum))
	}
	if _, ok := e.Driver("H1"); ok {
		t.Fatal("H1 should be forgotten once its slot is reused")
	}
	if _, ok := e.Driver("H2"); !ok {
		t.Fatal("H2 has been offline for under an hour and should still be known")
	}
	if v, _ := e.Driver("H3"); v.Num != 0 || v.Trips != 0 || v.Earnings != 0 || v.State != Idle {
		t.Fatalf("H3 should start clean in slot 0, got %+v", v)
	}
}
