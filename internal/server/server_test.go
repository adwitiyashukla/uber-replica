package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/uber/h3-go/v4"

	"github.com/adwitiyashukla/uber-replica/internal/city"
	"github.com/adwitiyashukla/uber-replica/internal/config"
	"github.com/adwitiyashukla/uber-replica/internal/engine"
)

var (
	once    sync.Once
	loaded  *city.City
	loadErr error
)

func manhattan(t testing.TB) *city.City {
	once.Do(func() { loaded, loadErr = city.Load("../../data") })
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	return loaded
}

func start(t *testing.T) *httptest.Server {
	cfg := config.Default()
	cfg.Clock.Mode = "fixed"
	cfg.Clock.Start = "18:00"
	cfg.Clock.WarmupMin = 2
	cfg.Sim.DemandScale = 0.1
	cfg.Sim.FleetScale = 0.2
	cfg.Server.RatePerSecond = 1000
	cfg.Server.RateBurst = 1000
	cfg.Server.FrameMs = 200
	cfg.Server.WebDir = t.TempDir()
	c := manhattan(t)
	rt := NewRuntime(cfg, c, []byte("test"), nil, "-t-")
	srv, err := New(cfg, rt, c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go rt.Run(ctx)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		cancel()
	})
	return ts
}

func post(t *testing.T, url string, body any, out any) int {
	b, _ := json.Marshal(body)
	res, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func get(t *testing.T, url string, out any) int {
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func TestRideFlowOverHTTP(t *testing.T) {
	ts := start(t)
	var meta map[string]any
	if get(t, ts.URL+"/api/meta", &meta) != 200 || meta["replayDay"] != "2026-08-07" {
		t.Fatalf("meta: %v", meta)
	}
	var places struct{ Places []city.Place }
	get(t, ts.URL+"/api/places?q=grand%20central&lat=40.75&lng=-73.98", &places)
	if len(places.Places) == 0 {
		t.Fatal("search returned nothing")
	}
	rider := "R-tester01"
	if code := post(t, ts.URL+"/api/quote", map[string]any{"riderId": "nope", "from": timesSq, "to": unionSq}, nil); code != 400 {
		t.Fatalf("bad rider id should be 400, got %d", code)
	}
	var q struct {
		Quote engine.QuoteResult `json:"quote"`
		Route [][2]float64       `json:"route"`
	}
	body := map[string]any{"riderId": rider, "from": timesSq, "to": unionSq, "fromName": "times square", "toName": "buy cheap watches"}
	if code := post(t, ts.URL+"/api/quote", body, &q); code != 200 {
		t.Fatalf("quote failed with %d", code)
	}
	if len(q.Quote.Options) != 3 || len(q.Route) < 2 || q.Quote.Options[0].Fare.Total <= 0 {
		t.Fatalf("bad quote %+v", q)
	}
	if q.Quote.Pickup.Name != "Times Square" || strings.Contains(q.Quote.Dropoff.Name, "watches") {
		t.Fatalf("place names should come from the place index, got %q and %q", q.Quote.Pickup.Name, q.Quote.Dropoff.Name)
	}
	var trip engine.TripView
	if code := post(t, ts.URL+"/api/rides", map[string]any{"riderId": rider, "token": q.Quote.Options[0].Token, "ref": "a1"}, &trip); code != 200 {
		t.Fatalf("request failed with %d", code)
	}
	var again engine.TripView
	post(t, ts.URL+"/api/rides", map[string]any{"riderId": rider, "token": q.Quote.Options[0].Token, "ref": "a1"}, &again)
	if again.ID != trip.ID {
		t.Fatalf("retrying with the same ref made a second trip: %s vs %s", trip.ID, again.ID)
	}
	if trip.Pickup.Name != "Times Square" {
		t.Fatalf("the trip should keep the quoted pickup name, got %q", trip.Pickup.Name)
	}
	var active engine.TripView
	if get(t, ts.URL+"/api/riders/"+rider+"/active", &active) != 200 || active.ID != trip.ID {
		t.Fatalf("active trip missing")
	}
	var cancelled engine.TripView
	if code := post(t, ts.URL+"/api/rides/"+trip.ID+"/cancel", map[string]any{"riderId": "R-someoneelse"}, nil); code != 403 {
		t.Fatalf("another rider cancelling should be 403, got %d", code)
	}
	post(t, ts.URL+"/api/rides/"+trip.ID+"/cancel", map[string]any{"riderId": rider}, &cancelled)
	if cancelled.State != engine.Cancelled {
		t.Fatalf("trip should be cancelled, is %v", cancelled.State)
	}
}

func TestDriverGoesOnline(t *testing.T) {
	ts := start(t)
	id := "H-tester01"
	if get(t, ts.URL+"/api/drivers/"+id, nil) != http.StatusNoContent {
		t.Fatal("unknown driver should be 204")
	}
	var v engine.DriverView
	if code := post(t, ts.URL+"/api/drivers/"+id+"/online", map[string]any{"product": "comfort"}, &v); code != 200 {
		t.Fatalf("online failed with %d", code)
	}
	if v.State != engine.Idle || v.Product != engine.Comfort || v.Simulated {
		t.Fatalf("unexpected driver %+v", v)
	}
	if code := post(t, ts.URL+"/api/drivers/"+id+"/accept", map[string]any{"offerId": "O1"}, nil); code != 409 {
		t.Fatalf("accepting a missing offer should be 409, got %d", code)
	}
	post(t, ts.URL+"/api/drivers/"+id+"/offline", map[string]any{}, &v)
	if v.State != engine.Offline {
		t.Fatalf("driver should be offline, is %v", v.State)
	}
}

func TestOpsSocketStreamsFrames(t *testing.T) {
	ts := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws?role=ops", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 22)
	seen := map[string]bool{}
	sub, _ := json.Marshal(map[string]any{"t": "view", "bbox": []float64{-74.03, 40.69, -73.9, 40.89}})
	if err := conn.Write(ctx, websocket.MessageText, sub); err != nil {
		t.Fatal(err)
	}
	for !seen["frame"] || !seen["hello"] || !seen["stats"] {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v (seen %v)", err, seen)
		}
		if typ == websocket.MessageBinary {
			n := binary.LittleEndian.Uint32(data[9:])
			if data[0] != 1 || len(data) != headerBytes+int(n)*recordBytes || n == 0 {
				t.Fatalf("bad frame: %d bytes, %d cars", len(data), n)
			}
			seen["frame"] = true
			continue
		}
		var m struct{ T string }
		json.Unmarshal(data, &m)
		seen[m.T] = true
	}
}

func TestRejectsBadSocketRole(t *testing.T) {
	ts := start(t)
	for _, q := range []string{"?role=admin", "?role=rider&id=x", "?role=driver&id=S0000001"} {
		if code := get(t, ts.URL+"/ws"+q, nil); code != 400 {
			t.Fatalf("%s should be 400, got %d", q, code)
		}
	}
}

func TestFrameEncoding(t *testing.T) {
	pos := []engine.Position{
		{Num: 7, Lat: 40.758, Lng: -73.9855, Heading: 90.5, State: engine.OnTrip, Product: engine.XL},
		{Num: 9, Lat: 40.8116, Lng: -73.9465, Heading: 359.99, State: engine.Idle},
	}
	groups := EncodeGroups(pos)
	cells := map[h3.Cell]bool{}
	for c := range groups {
		cells[c] = true
	}
	msg := Assemble(123456, groups, cells)
	if len(msg) != headerBytes+2*recordBytes {
		t.Fatalf("frame length %d", len(msg))
	}
	if at := math.Float64frombits(binary.LittleEndian.Uint64(msg[1:])); at != 123456 {
		t.Fatalf("time %v", at)
	}
	found := false
	for o := headerBytes; o < len(msg); o += recordBytes {
		if binary.LittleEndian.Uint32(msg[o:]) == 7 {
			found = true
			lat := float64(int32(binary.LittleEndian.Uint32(msg[o+4:]))) / 1e6
			if math.Abs(lat-40.758) > 1e-6 || msg[o+14] != byte(engine.OnTrip) || msg[o+15] != byte(engine.XL) {
				t.Fatalf("record decoded wrong")
			}
		}
	}
	if !found {
		t.Fatal("car 7 missing from the frame")
	}
	if part := Assemble(1, groups, map[h3.Cell]bool{}); len(part) != headerBytes {
		t.Fatalf("an empty subscription should get an empty frame, got %d bytes", len(part))
	}
}

func TestOpsChangesResetAfterQuietMinutes(t *testing.T) {
	cfg := config.Default()
	cfg.Clock.Mode = "fixed"
	cfg.Clock.Start = "18:00"
	cfg.Clock.WarmupMin = 0
	cfg.Sim.DemandScale = 0.1
	cfg.Sim.FleetScale = 0.2
	rt := NewRuntime(cfg, manhattan(t), []byte("test"), nil, "-t-")
	if err := rt.eng.SetPolicy("greedy", false); err != nil {
		t.Fatal(err)
	}
	rt.SetSpeed(5)
	rt.touchOps()
	rt.resetOps()
	if p, _ := rt.eng.Policy(); p != "greedy" || rt.clock.Speed() != 5 {
		t.Fatal("changes should stay while they are fresh")
	}
	rt.opsAt = time.Now().Add(-time.Duration(cfg.Server.OpsResetMin+1) * time.Minute)
	rt.resetOps()
	if p, b2b := rt.eng.Policy(); p != cfg.Dispatch.Policy || b2b != cfg.Dispatch.BackToBack || rt.clock.Speed() != cfg.Clock.Speed {
		t.Fatalf("expected the defaults back, got %s %v %v", p, b2b, rt.clock.Speed())
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(1, 3)
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !l.allow("a", now) {
			t.Fatal("burst should pass")
		}
	}
	if l.allow("a", now) {
		t.Fatal("fourth call should be limited")
	}
	if !l.allow("b", now) {
		t.Fatal("other clients have their own bucket")
	}
	if !l.allow("a", now.Add(1100*time.Millisecond)) {
		t.Fatal("tokens should refill")
	}
}

var (
	timesSq = map[string]float64{"lat": 40.758, "lng": -73.9855}
	unionSq = map[string]float64{"lat": 40.7359, "lng": -73.9911}
)
