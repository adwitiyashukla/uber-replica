package server

import (
	"fmt"
	"math"

	"github.com/adwitiyashukla/uber-replica/internal/city"
	"github.com/adwitiyashukla/uber-replica/internal/engine"
	"github.com/adwitiyashukla/uber-replica/internal/geo"
	"github.com/adwitiyashukla/uber-replica/internal/roads"
	"github.com/uber/h3-go/v4"
)

type feedItem struct {
	At    int64   `json:"at"`
	Kind  string  `json:"kind"`
	Text  string  `json:"text"`
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
	Human bool    `json:"human"`
}

type feed struct {
	c     *city.City
	items []feedItem
}

func newFeed(c *city.City) *feed { return &feed{c: c} }

func (f *feed) add(it feedItem) {
	f.items = append(f.items, it)
	if len(f.items) > 64 {
		f.items = append(f.items[:0], f.items[len(f.items)-32:]...)
	}
}

func (f *feed) take() []feedItem {
	var human, rest []feedItem
	perKind := map[string]int{}
	for i := len(f.items) - 1; i >= 0; i-- {
		it := f.items[i]
		if it.Human {
			human = append(human, it)
			continue
		}
		if perKind[it.Kind] < 2 && len(rest) < 6 {
			perKind[it.Kind]++
			rest = append(rest, it)
		}
	}
	f.items = f.items[:0]
	out := append(human, rest...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func money(cents int64) string { return fmt.Sprintf("$%.2f", float64(cents)/100) }

func minutes(sec float64) string {
	m := int(math.Round(sec / 60))
	if m <= 1 {
		return "1 min"
	}
	return fmt.Sprintf("%d min", m)
}

func (f *feed) observe(ev *engine.Event) {
	t := ev.Trip
	human := t != nil && !t.Simulated
	if t != nil && t.Driver != nil && !t.Driver.Simulated {
		human = true
	}
	item := feedItem{At: ev.At, Kind: ev.Kind.String(), Human: human}
	if t != nil {
		item.Lat, item.Lng = t.Pickup.Pt.Lat, t.Pickup.Pt.Lng
	}
	switch ev.Kind {
	case engine.TripRequested:
		label := "Request"
		if t.Product != engine.Standard {
			label = map[engine.Product]string{engine.Comfort: "Comfort request", engine.XL: "XL request"}[t.Product]
		}
		item.Text = fmt.Sprintf("%s from %s to %s", label, f.c.ZoneName(t.Pickup.Zone), f.c.ZoneName(t.Dropoff.Zone))
	case engine.TripAccepted:
		how := "pickup in " + minutes(t.ETA)
		if t.B2B {
			how = "back-to-back, pickup in " + minutes(t.ETA)
		}
		item.Text = fmt.Sprintf("%s accepted, %s", t.Driver.Name, how)
	case engine.TripCompleted:
		item.Text = fmt.Sprintf("Dropped off in %s, %s fare", f.c.ZoneName(t.Dropoff.Zone), money(t.Fare.Total))
		item.Lat, item.Lng = t.Dropoff.Pt.Lat, t.Dropoff.Pt.Lng
	case engine.TripCancelled:
		if ev.Reason == "no_drivers" {
			item.Text = "No car found near " + f.c.ZoneName(t.Pickup.Zone)
		} else {
			item.Text = "Rider cancelled near " + f.c.ZoneName(t.Pickup.Zone)
			if t.CancelFee > 0 {
				item.Text += ", " + money(t.CancelFee) + " fee"
			}
		}
	case engine.TripReopened:
		item.Text = "Driver dropped a trip, finding another car near " + f.c.ZoneName(t.Pickup.Zone)
	case engine.DriverOnline:
		if ev.Driver == nil || ev.Driver.Simulated {
			return
		}
		item.Human = true
		item.Lat, item.Lng = ev.Driver.Lat, ev.Driver.Lng
		item.Text = fmt.Sprintf("A visitor went online as %s", ev.Driver.Name)
	case engine.SurgeChanged:
		for _, c := range ev.Surge {
			if c.Value < 1.5 {
				continue
			}
			p := geo.Center(h3.Cell(h3.IndexFromString(c.Cell)))
			z := f.c.Graph.Zone[f.c.Graph.Nearest(p)]
			f.add(feedItem{At: ev.At, Kind: "surge", Text: fmt.Sprintf("Surge %.1fx near %s", c.Value, f.c.ZoneName(z)), Lat: p.Lat, Lng: p.Lng})
		}
		return
	default:
		return
	}
	f.add(item)
}

type RouteView struct {
	DriverID string       `json:"driverId"`
	TripID   string       `json:"tripId,omitempty"`
	Purpose  string       `json:"purpose"`
	Points   [][2]float64 `json:"points"`
	Steps    []roads.Step `json:"steps"`
	Meters   float64      `json:"meters"`
	Seconds  float64      `json:"seconds"`
}

func routeView(g *roads.Graph, rt *engine.Route, trip *engine.TripView) RouteView {
	v := RouteView{DriverID: rt.DriverID, Purpose: rt.Purpose, Meters: rt.Path.Meters, Seconds: rt.Path.Seconds, Steps: g.Directions(rt.Path)}
	if trip != nil {
		v.TripID = trip.ID
	}
	v.Points = make([][2]float64, len(rt.Track.Pts))
	for i, p := range rt.Track.Pts {
		v.Points[i] = [2]float64{round(p.Lng, 6), round(p.Lat, 6)}
	}
	return v
}
