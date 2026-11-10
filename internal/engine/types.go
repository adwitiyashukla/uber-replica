package engine

import (
	"errors"
	"fmt"

	"github.com/uber/h3-go/v4"

	"github.com/adwitiyashukla/uber-replica/internal/geo"
	"github.com/adwitiyashukla/uber-replica/internal/roads"
)

type Product uint8

const (
	Standard Product = iota
	Comfort
	XL
)

var Products = []Product{Standard, Comfort, XL}

var productNames = [...]string{"standard", "comfort", "xl"}

func (p Product) String() string { return productNames[p] }

func ParseProduct(s string) (Product, error) {
	for i, n := range productNames {
		if n == s {
			return Product(i), nil
		}
	}
	return 0, fmt.Errorf("unknown product %q", s)
}

func (p Product) MarshalText() ([]byte, error) { return []byte(p.String()), nil }

func (p *Product) UnmarshalText(b []byte) error {
	v, err := ParseProduct(string(b))
	*p = v
	return err
}

func (vehicle Product) Serves(trip Product) bool {
	return trip == Standard || vehicle == trip
}

type DriverState uint8

const (
	Offline DriverState = iota
	Idle
	Offered
	EnRoute
	AtPickup
	OnTrip
)

var driverStateNames = [...]string{"offline", "idle", "offered", "en_route", "at_pickup", "on_trip"}

func (s DriverState) String() string { return driverStateNames[s] }

func (s DriverState) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

func (s *DriverState) UnmarshalText(b []byte) error {
	for i, n := range driverStateNames {
		if n == string(b) {
			*s = DriverState(i)
			return nil
		}
	}
	return fmt.Errorf("unknown driver state %q", b)
}

func (s DriverState) Busy() bool { return s == EnRoute || s == AtPickup || s == OnTrip }

type TripState uint8

const (
	Searching TripState = iota
	Offering
	Accepted
	Arrived
	Riding
	Completed
	Cancelled
)

var tripStateNames = [...]string{"searching", "offering", "accepted", "arrived", "on_trip", "completed", "cancelled"}

func (s TripState) String() string { return tripStateNames[s] }

func (s TripState) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

func (s *TripState) UnmarshalText(b []byte) error {
	for i, n := range tripStateNames {
		if n == string(b) {
			*s = TripState(i)
			return nil
		}
	}
	return fmt.Errorf("unknown trip state %q", b)
}

func (s TripState) Open() bool { return s == Searching || s == Offering }

func (s TripState) Done() bool { return s == Completed || s == Cancelled }

var (
	ErrUnknownDriver  = errors.New("unknown driver")
	ErrUnknownTrip    = errors.New("unknown trip")
	ErrNoOffer        = errors.New("no such offer for this driver")
	ErrOfferExpired   = errors.New("offer expired")
	ErrBadState       = errors.New("not allowed in the current state")
	ErrNotHere        = errors.New("driver is not close enough")
	ErrRiderBusy      = errors.New("rider already has an active trip")
	ErrBadQuote       = errors.New("quote is invalid")
	ErrQuoteExpired   = errors.New("quote expired")
	ErrNotYourTrip    = errors.New("trip belongs to another rider")
	ErrDriverOnline   = errors.New("driver is already online")
	ErrSameLocation   = errors.New("pickup and dropoff are the same place")
	ErrBadRating      = errors.New("rating must be 1 to 5")
	ErrAlreadyRated   = errors.New("trip already rated")
	ErrOutsideService = errors.New("location is outside the service area")
)

type DriverInfo struct {
	ID        string
	Name      string
	Car       string
	Plate     string
	Product   Product
	Rating    float64
	Simulated bool
}

type Driver struct {
	DriverInfo
	Num     uint32
	State   DriverState
	Pos     geo.Point
	Heading float64
	Node    uint32

	Trip  *Trip
	Next  *Trip
	Offer *Offer

	route    *roads.Track
	routeSec float64
	routeSeg int
	moved    bool

	cell    h3.Cell
	cellPt  geo.Point
	cellPos int
	b2bCell h3.Cell
	b2bPos  int
	inIdle  bool
	inB2B   bool

	OnlineAt  int64
	offAt     int64
	busyFrom  int64
	BusyMs    int64
	OnlineMs  int64
	Earnings  int64
	Trips     int
	GoOffline bool
}

type Place struct {
	Node uint32    `json:"-"`
	Name string    `json:"name"`
	Zone uint16    `json:"zone"`
	Pt   geo.Point `json:"point"`
}

type Fare struct {
	Base         int64   `json:"base"`
	Surge        float64 `json:"surge"`
	SurgeAmount  int64   `json:"surgeAmount"`
	Subtotal     int64   `json:"subtotal"`
	SalesTax     int64   `json:"salesTax"`
	BlackCarFund int64   `json:"blackCarFund"`
	Congestion   int64   `json:"congestion"`
	CBD          int64   `json:"cbd"`
	Total        int64   `json:"total"`
}

type Trip struct {
	ID        string
	seq       uint64
	RiderID   string
	Simulated bool
	ref       string
	Product   Product
	Pickup    Place
	Dropoff   Place
	State     TripState
	Fare      Fare
	DriverPay int64
	Meters    float64
	Seconds   float64

	RequestedAt int64
	OfferedAt   int64
	AcceptedAt  int64
	ArrivedAt   int64
	StartedAt   int64
	EndedAt     int64

	Driver   *Driver
	Offer    *Offer
	declined []string
	Offers   int
	B2B      bool

	PickupETA    float64
	CancelReason string
	CancelFee    int64
	Rating       int
}

func (t *Trip) declinedBy(id string) bool {
	for _, d := range t.declined {
		if d == id {
			return true
		}
	}
	return false
}

type Offer struct {
	ID        string
	Trip      *Trip
	Driver    *Driver
	SentAt    int64
	ExpiresAt int64
	ETA       float64
	B2B       bool
}

type Kind uint8

const (
	TripRequested Kind = iota + 1
	OfferSent
	OfferClosed
	TripAccepted
	DriverRouted
	DriverArrived
	TripStarted
	TripCompleted
	TripCancelled
	DriverOnline
	DriverOffline
	SurgeChanged
	Posted
	TripReopened
	TripRated
)

var kindNames = [...]string{"", "trip_requested", "offer_sent", "offer_closed", "trip_accepted", "driver_routed",
	"driver_arrived", "trip_started", "trip_completed", "trip_cancelled", "driver_online", "driver_offline",
	"surge_changed", "posted", "trip_reopened", "trip_rated"}

func (k Kind) String() string { return kindNames[k] }

func (k Kind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

type Route struct {
	DriverID string
	Purpose  string
	Path     roads.Path
	Track    *roads.Track
}

type SurgeCell struct {
	Cell  string  `json:"cell"`
	Value float64 `json:"value"`
}

type Line struct {
	Account string `json:"account"`
	Cents   int64  `json:"cents"`
}

type Journal struct {
	ID     string `json:"id"`
	TripID string `json:"tripId"`
	At     int64  `json:"at"`
	Kind   string `json:"kind"`
	Lines  []Line `json:"lines"`
}

type Event struct {
	Seq      uint64
	At       int64
	Kind     Kind
	Trip     *TripView
	Offer    *OfferView
	Driver   *DriverCard
	DriverID string
	RiderID  string
	Reason   string
	Route    *Route
	Surge    []SurgeCell
	Journal  *Journal
}
