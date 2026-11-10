package engine

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math"
	"strings"

	"github.com/adwitiyashukla/uber-replica/internal/geo"
	"github.com/adwitiyashukla/uber-replica/internal/roads"
)

type QuoteOption struct {
	Product   Product `json:"product"`
	Fare      Fare    `json:"fare"`
	DriverPay int64   `json:"driverPay"`
	PickupETA float64 `json:"pickupEta"`
	Token     string  `json:"token"`
	ExpiresAt int64   `json:"expiresAt"`
}

type QuoteResult struct {
	Pickup  Place         `json:"pickup"`
	Dropoff Place         `json:"dropoff"`
	Meters  float64       `json:"meters"`
	Seconds float64       `json:"seconds"`
	Surge   float64       `json:"surge"`
	Options []QuoteOption `json:"options"`
	Path    roads.Path    `json:"-"`
}

type claims struct {
	R string  `json:"r"`
	P Product `json:"p"`
	A uint32  `json:"a"`
	B uint32  `json:"b"`
	F Fare    `json:"f"`
	D int64   `json:"d"`
	M float64 `json:"m"`
	S float64 `json:"s"`
	X int64   `json:"x"`
	U string  `json:"u,omitempty"`
	V string  `json:"v,omitempty"`
}

func (e *Engine) multiplier(p Product) float64 {
	switch p {
	case Comfort:
		return e.cfgP.ComfortMultiplier
	case XL:
		return e.cfgP.XLMultiplier
	}
	return 1
}

func cents(dollars float64) int64 { return int64(math.Round(dollars * 100)) }

func (e *Engine) Price(p Product, meters, seconds float64, zones []uint16, surge float64) (Fare, int64) {
	cal := e.city.Cal
	miles, minutes := meters/1609.344, seconds/60
	base := math.Max(cal.Fare.Base+cal.Fare.PerMile*miles+cal.Fare.PerMinute*minutes, cal.Fare.Min)
	f := Fare{Base: cents(base * e.multiplier(p)), Surge: surge}
	f.Subtotal = int64(math.Round(float64(f.Base) * surge))
	f.SurgeAmount = f.Subtotal - f.Base
	f.SalesTax = int64(math.Round(float64(f.Subtotal) * cal.Fees.SalesTaxRate))
	f.BlackCarFund = int64(math.Round(float64(f.Subtotal) * cal.Fees.BlackCarFundRate))
	for _, z := range zones {
		if e.city.South96(z) {
			f.Congestion = cents(cal.Fees.CongestionSurcharge)
		}
		if e.city.CBD(z) {
			f.CBD = cents(cal.Fees.CBDFee)
		}
	}
	f.Total = f.Subtotal + f.SalesTax + f.BlackCarFund + f.Congestion + f.CBD
	pay := cal.DriverPay.Base + cal.DriverPay.PerMile*miles + cal.DriverPay.PerMinute*minutes
	pay *= e.multiplier(p) * (1 + e.cfgP.DriverSurgeShare*(surge-1))
	return f, cents(pay)
}

func (e *Engine) Quote(riderID string, from, to geo.Point) (QuoteResult, error) {
	return e.QuoteNamed(riderID, from, to, "", "")
}

func (e *Engine) QuoteNamed(riderID string, from, to geo.Point, fromName, toName string) (QuoteResult, error) {
	pn, ok1 := e.inService(from)
	dn, ok2 := e.inService(to)
	if !ok1 || !ok2 {
		return QuoteResult{}, ErrOutsideService
	}
	if pn == dn {
		return QuoteResult{}, ErrSameLocation
	}
	path, ok := e.router.Route(pn, dn)
	if !ok {
		return QuoteResult{}, ErrOutsideService
	}
	seconds := path.Seconds * e.Factor()
	zones := e.g.PathZones(path)
	pickup := e.place(pn)
	etas := e.pickupETAs(pickup, float64(e.cfgD.B2BMaxRemainingS))
	surge := 1.0
	if e.cfgS.Enabled {
		surge = e.surge.at(pickup.Pt)
		eta := etas[Standard]
		if eta < 0 {
			eta = float64(e.cfgD.MaxPickupS)
		}
		e.surge.observe(pickup.Pt, eta)
	}
	q := QuoteResult{Pickup: named(pickup, fromName), Dropoff: named(e.place(dn), toName), Meters: path.Meters, Seconds: seconds, Surge: surge, Path: path}
	exp := e.now + int64(e.cfgP.QuoteTTLS)*1000
	for _, p := range Products {
		fare, pay := e.Price(p, path.Meters, seconds, zones, surge)
		c := claims{R: riderID, P: p, A: pn, B: dn, F: fare, D: pay, M: path.Meters, S: seconds, X: exp, U: fromName, V: toName}
		q.Options = append(q.Options, QuoteOption{Product: p, Fare: fare, DriverPay: pay, PickupETA: etas[p], Token: e.sign(c), ExpiresAt: exp})
	}
	return q, nil
}

func (e *Engine) FillETAs(q *QuoteResult) {
	var later [3]float64
	found := false
	for i, o := range q.Options {
		if o.PickupETA >= 0 {
			continue
		}
		if !found {
			later, found = e.pickupETAs(q.Pickup, math.Inf(1)), true
		}
		q.Options[i].PickupETA = later[o.Product]
	}
}

func (e *Engine) pickupETAs(pickup Place, horizon float64) [3]float64 {
	out := [3]float64{-1, -1, -1}
	var pool []candidate
	limit := 2 * e.cfgD.MaxCandidates
	for k, ring := range geo.Rings(geo.Cell(pickup.Pt, indexRes), e.cfgD.SearchRings) {
		for _, c := range ring {
			for _, d := range e.idle[c] {
				pool = append(pool, candidate{d: d})
			}
			if !e.backToBack {
				continue
			}
			for _, d := range e.b2b[c] {
				if d.Next == nil && d.Offer == nil && e.remaining(d) <= horizon {
					pool = append(pool, candidate{d: d, b2b: true})
				}
			}
		}
		if len(pool) >= limit && k >= 1 {
			break
		}
	}
	if len(pool) == 0 {
		return out
	}
	if len(pool) > limit {
		pool = pool[:limit]
	}
	targets := make([]uint32, len(pool))
	for i, c := range pool {
		targets[i] = e.node(c.d)
		if c.b2b {
			targets[i] = c.d.Trip.Dropoff.Node
		}
	}
	f := e.Factor()
	times := e.router.TimesTo(pickup.Node, targets, float64(e.cfgD.MaxPickupS)/f)
	for i, c := range pool {
		if math.IsInf(times[i], 1) {
			continue
		}
		eta := times[i] * f
		if c.b2b {
			eta += e.remaining(c.d) + e.cfgD.DropoffDwellS
		}
		for _, p := range Products {
			if c.d.Product.Serves(p) && (out[p] < 0 || eta < out[p]) {
				out[p] = eta
			}
		}
	}
	return out
}

func (e *Engine) sign(c claims) string {
	body, _ := json.Marshal(c)
	payload := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, e.secret)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (e *Engine) verify(token, riderID string) (claims, error) {
	var c claims
	payload, sig, ok := strings.Cut(token, ".")
	if !ok {
		return c, ErrBadQuote
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return c, ErrBadQuote
	}
	mac := hmac.New(sha256.New, e.secret)
	mac.Write([]byte(payload))
	if !hmac.Equal(got, mac.Sum(nil)) {
		return c, ErrBadQuote
	}
	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(body, &c) != nil {
		return c, ErrBadQuote
	}
	if c.R != riderID || int(c.A) >= e.g.N() || int(c.B) >= e.g.N() || int(c.P) >= len(Products) {
		return c, ErrBadQuote
	}
	if e.now > c.X {
		return c, ErrQuoteExpired
	}
	return c, nil
}
