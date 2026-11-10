package engine

import (
	"hash/fnv"
	"math"
	"strconv"

	"github.com/adwitiyashukla/uber-replica/internal/geo"
	"github.com/adwitiyashukla/uber-replica/internal/roads"
)

type DriverCard struct {
	ID        string      `json:"id"`
	Num       uint32      `json:"num"`
	Name      string      `json:"name"`
	Car       string      `json:"car"`
	Plate     string      `json:"plate"`
	Product   Product     `json:"product"`
	Rating    float64     `json:"rating"`
	State     DriverState `json:"state"`
	Lat       float64     `json:"lat"`
	Lng       float64     `json:"lng"`
	Heading   float64     `json:"heading"`
	Simulated bool        `json:"simulated"`
}

type TripView struct {
	ID           string      `json:"id"`
	RiderID      string      `json:"riderId"`
	Simulated    bool        `json:"simulated"`
	Product      Product     `json:"product"`
	State        TripState   `json:"state"`
	Pickup       Place       `json:"pickup"`
	Dropoff      Place       `json:"dropoff"`
	Fare         Fare        `json:"fare"`
	DriverPay    int64       `json:"driverPay"`
	Meters       float64     `json:"meters"`
	Seconds      float64     `json:"seconds"`
	RequestedAt  int64       `json:"requestedAt"`
	AcceptedAt   int64       `json:"acceptedAt,omitempty"`
	ArrivedAt    int64       `json:"arrivedAt,omitempty"`
	StartedAt    int64       `json:"startedAt,omitempty"`
	EndedAt      int64       `json:"endedAt,omitempty"`
	Driver       *DriverCard `json:"driver,omitempty"`
	ETA          float64     `json:"eta"`
	B2B          bool        `json:"b2b"`
	Offers       int         `json:"offers"`
	CancelReason string      `json:"cancelReason,omitempty"`
	CancelFee    int64       `json:"cancelFee,omitempty"`
	Rating       int         `json:"rating,omitempty"`
}

type OfferView struct {
	ID          string  `json:"id"`
	TripID      string  `json:"tripId"`
	DriverID    string  `json:"driverId"`
	RiderID     string  `json:"riderId"`
	SentAt      int64   `json:"sentAt"`
	ExpiresAt   int64   `json:"expiresAt"`
	ETA         float64 `json:"eta"`
	B2B         bool    `json:"b2b"`
	Product     Product `json:"product"`
	Pickup      Place   `json:"pickup"`
	Dropoff     Place   `json:"dropoff"`
	Earnings    int64   `json:"earnings"`
	Fare        int64   `json:"fare"`
	Surge       float64 `json:"surge"`
	TripSeconds float64 `json:"tripSeconds"`
	TripMeters  float64 `json:"tripMeters"`
}

type DriverView struct {
	DriverCard
	Trip      *TripView  `json:"trip,omitempty"`
	Next      *TripView  `json:"next,omitempty"`
	Offer     *OfferView `json:"offer,omitempty"`
	Earnings  int64      `json:"earnings"`
	Trips     int        `json:"trips"`
	OnlineMs  int64      `json:"onlineMs"`
	GoOffline bool       `json:"goOffline"`
}

type Position struct {
	Num     uint32
	Lat     float64
	Lng     float64
	Heading float64
	State   DriverState
	Product Product
}

func (e *Engine) card(d *Driver) DriverCard {
	return DriverCard{
		ID: d.ID, Num: d.Num, Name: d.Name, Car: d.Car, Plate: d.Plate, Product: d.Product,
		Rating: d.Rating, State: d.State, Lat: d.Pos.Lat, Lng: d.Pos.Lng, Heading: d.Heading, Simulated: d.Simulated,
	}
}

func (e *Engine) eta(t *Trip) float64 {
	d := t.Driver
	switch t.State {
	case Accepted:
		if d.Trip == t && d.route != nil {
			return e.remaining(d)
		}
		return math.Max(30, t.PickupETA-float64(e.now-t.AcceptedAt)/1000)
	case Riding:
		if d.route != nil {
			return e.remaining(d)
		}
	}
	return -1
}

func (e *Engine) view(t *Trip) TripView {
	v := TripView{
		ID: t.ID, RiderID: t.RiderID, Simulated: t.Simulated, Product: t.Product, State: t.State,
		Pickup: t.Pickup, Dropoff: t.Dropoff, Fare: t.Fare, DriverPay: t.DriverPay, Meters: t.Meters,
		Seconds: t.Seconds, RequestedAt: t.RequestedAt, AcceptedAt: t.AcceptedAt, ArrivedAt: t.ArrivedAt,
		StartedAt: t.StartedAt, EndedAt: t.EndedAt, ETA: e.eta(t), B2B: t.B2B, Offers: t.Offers,
		CancelReason: t.CancelReason, CancelFee: t.CancelFee, Rating: t.Rating,
	}
	if t.Driver != nil {
		c := e.card(t.Driver)
		v.Driver = &c
	}
	return v
}

func (e *Engine) offerView(o *Offer) *OfferView {
	t := o.Trip
	return &OfferView{
		ID: o.ID, TripID: t.ID, DriverID: o.Driver.ID, RiderID: t.RiderID, SentAt: o.SentAt, ExpiresAt: o.ExpiresAt,
		ETA: o.ETA, B2B: o.B2B, Product: t.Product, Pickup: t.Pickup, Dropoff: t.Dropoff, Earnings: t.DriverPay,
		Fare: t.Fare.Total, Surge: t.Fare.Surge, TripSeconds: t.Seconds, TripMeters: t.Meters,
	}
}

func (e *Engine) driverView(d *Driver) DriverView {
	v := DriverView{DriverCard: e.card(d), Earnings: d.Earnings, Trips: d.Trips, OnlineMs: d.OnlineMs, GoOffline: d.GoOffline}
	if d.State != Offline {
		v.OnlineMs += e.now - d.OnlineAt
	}
	if d.Trip != nil {
		t := e.view(d.Trip)
		v.Trip = &t
	}
	if d.Next != nil {
		t := e.view(d.Next)
		v.Next = &t
	}
	if d.Offer != nil {
		v.Offer = e.offerView(d.Offer)
	}
	return v
}

func (e *Engine) Trip(id string) (TripView, bool) {
	t := e.trips[id]
	if t == nil {
		return TripView{}, false
	}
	return e.view(t), true
}

func (e *Engine) ActiveTrip(riderID string) (TripView, bool) {
	t := e.active[riderID]
	if t == nil {
		return TripView{}, false
	}
	return e.view(t), true
}

func (e *Engine) Driver(id string) (DriverView, bool) {
	d := e.drivers[id]
	if d == nil {
		return DriverView{}, false
	}
	return e.driverView(d), true
}

func (e *Engine) DriverByNum(n uint32) (DriverCard, bool) {
	if int(n) >= len(e.byNum) {
		return DriverCard{}, false
	}
	return e.card(e.byNum[n]), true
}

func (e *Engine) DriverRoute(id string) (*roads.Track, float64, bool) {
	d := e.drivers[id]
	if d == nil || d.route == nil {
		return nil, 0, false
	}
	return d.route, d.routeSec, true
}

func (e *Engine) Positions(dst []Position) []Position {
	dst = dst[:0]
	for _, d := range e.byNum {
		if d.State == Offline {
			continue
		}
		dst = append(dst, Position{Num: d.Num, Lat: d.Pos.Lat, Lng: d.Pos.Lng, Heading: d.Heading, State: d.State, Product: d.Product})
	}
	return dst
}

func (e *Engine) PickupPoint(p geo.Point) (Place, bool) {
	n, ok := e.inService(p)
	if !ok {
		return Place{}, false
	}
	return e.place(n), true
}

func Fingerprint(events []Event) uint64 {
	h := fnv.New64a()
	var buf []byte
	for _, ev := range events {
		buf = buf[:0]
		buf = strconv.AppendUint(buf, ev.Seq, 10)
		buf = strconv.AppendInt(buf, ev.At, 10)
		buf = append(buf, byte(ev.Kind))
		buf = append(buf, ev.DriverID...)
		buf = append(buf, ev.RiderID...)
		buf = append(buf, ev.Reason...)
		if ev.Trip != nil {
			buf = append(buf, ev.Trip.ID...)
			buf = append(buf, byte(ev.Trip.State))
			buf = strconv.AppendInt(buf, ev.Trip.Fare.Total, 10)
			buf = strconv.AppendFloat(buf, ev.Trip.ETA, 'f', 3, 64)
		}
		if ev.Offer != nil {
			buf = append(buf, ev.Offer.ID...)
			buf = strconv.AppendFloat(buf, ev.Offer.ETA, 'f', 3, 64)
		}
		if ev.Journal != nil {
			for _, l := range ev.Journal.Lines {
				buf = append(buf, l.Account...)
				buf = strconv.AppendInt(buf, l.Cents, 10)
			}
		}
		for _, c := range ev.Surge {
			buf = append(buf, c.Cell...)
			buf = strconv.AppendFloat(buf, c.Value, 'f', 1, 64)
		}
		h.Write(buf)
	}
	return h.Sum64()
}

func (e *Engine) HumansOnline() int {
	n := 0
	for _, d := range e.byNum {
		if !d.Simulated && d.State != Offline {
			n++
		}
	}
	return n
}
