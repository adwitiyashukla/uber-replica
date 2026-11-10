package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"hash/fnv"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/adwitiyashukla/uber-replica/internal/city"
	"github.com/adwitiyashukla/uber-replica/internal/config"
	"github.com/adwitiyashukla/uber-replica/internal/engine"
	"github.com/adwitiyashukla/uber-replica/internal/geo"
	"github.com/adwitiyashukla/uber-replica/internal/roads"
	"github.com/adwitiyashukla/uber-replica/internal/sim"
)

var (
	riderID  = regexp.MustCompile(`^R-[a-z0-9]{6,24}$`)
	driverID = regexp.MustCompile(`^H-[a-z0-9]{6,24}$`)
)

const maxClients = 600

type Server struct {
	cfg    config.Config
	rt     *Runtime
	city   *city.City
	mux    *http.ServeMux
	lim    *limiter
	zones  []byte
	roads  []byte
	bounds [4]float64
}

func New(cfg config.Config, rt *Runtime, c *city.City) (*Server, error) {
	s := &Server{cfg: cfg, rt: rt, city: c, mux: http.NewServeMux(), lim: newLimiter(cfg.Server.RatePerSecond, cfg.Server.RateBurst), bounds: c.Bounds()}
	var err error
	if s.zones, err = json.Marshal(c.Zones); err != nil {
		return nil, err
	}
	if s.roads, err = roadsGzip(c.Graph); err != nil {
		return nil, err
	}
	m := s.mux
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	m.HandleFunc("GET /api/meta", s.meta)
	m.HandleFunc("GET /api/zones", s.raw(s.zones, false))
	m.HandleFunc("GET /api/roads", s.raw(s.roads, true))
	m.HandleFunc("GET /api/places", s.places)
	m.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) { writeRaw(w, rt.StatsJSON()) })
	m.HandleFunc("GET /api/surge", func(w http.ResponseWriter, r *http.Request) { writeRaw(w, rt.SurgeJSON()) })
	m.HandleFunc("POST /api/quote", s.limit(s.quote))
	m.HandleFunc("POST /api/rides", s.limit(s.request))
	m.HandleFunc("GET /api/rides/{id}", s.trip)
	m.HandleFunc("GET /api/rides/{id}/events", s.tripEvents)
	m.HandleFunc("POST /api/rides/{id}/cancel", s.limit(s.cancel))
	m.HandleFunc("POST /api/rides/{id}/rate", s.limit(s.rate))
	m.HandleFunc("GET /api/riders/{id}/active", s.active)
	m.HandleFunc("GET /api/riders/{id}/history", s.history)
	m.HandleFunc("POST /api/drivers/{id}/online", s.limit(s.online))
	m.HandleFunc("POST /api/drivers/{id}/offline", s.limit(s.offline))
	m.HandleFunc("POST /api/drivers/{id}/accept", s.limit(s.offerAction(true)))
	m.HandleFunc("POST /api/drivers/{id}/decline", s.limit(s.offerAction(false)))
	m.HandleFunc("POST /api/drivers/{id}/start", s.limit(s.tripAction(true)))
	m.HandleFunc("POST /api/drivers/{id}/complete", s.limit(s.tripAction(false)))
	m.HandleFunc("GET /api/drivers/{id}", s.driver)
	m.HandleFunc("GET /api/drivers/{id}/route", s.driverRoute)
	m.HandleFunc("GET /api/cars/{num}", s.car)
	m.HandleFunc("POST /api/ops", s.limit(s.ops))
	m.HandleFunc("GET /ws", s.ws)
	m.Handle("/", s.spa())
	return s, nil
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Printf("panic in %s %s: %v", r.Method, r.URL.Path, v)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		s.mux.ServeHTTP(w, r)
	})
}

func (s *Server) limit(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.lim.allow(clientIP(r), time.Now()) {
			writeError(w, http.StatusTooManyRequests, errors.New("slow down a little"))
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func writeRaw(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, engine.ErrUnknownTrip), errors.Is(err, engine.ErrUnknownDriver):
		return http.StatusNotFound
	case errors.Is(err, engine.ErrNotYourTrip):
		return http.StatusForbidden
	case errors.Is(err, engine.ErrBadQuote), errors.Is(err, engine.ErrSameLocation),
		errors.Is(err, engine.ErrOutsideService), errors.Is(err, engine.ErrBadRating):
		return http.StatusBadRequest
	}
	return http.StatusConflict
}

func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("bad json body"))
		return false
	}
	return true
}

func (s *Server) raw(b []byte, gz bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if gz {
			w.Header().Set("Content-Encoding", "gzip")
		}
		w.Write(b)
	}
}

func roadsGzip(g *roads.Graph) ([]byte, error) {
	type out struct {
		Lines   [][]float64 `json:"lines"`
		Classes []uint8     `json:"classes"`
	}
	var o out
	for e := 0; e < g.M(); e++ {
		u, v := g.From[e], g.To[e]
		if u > v && hasEdge(g, v, u) {
			continue
		}
		var line []float64
		for _, p := range g.EdgePoints(uint32(e)) {
			line = append(line, round(p.Lng, 5), round(p.Lat, 5))
		}
		o.Lines = append(o.Lines, line)
		o.Classes = append(o.Classes, g.Class[e])
	}
	b, err := json.Marshal(o)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(b)
	zw.Close()
	return buf.Bytes(), nil
}

func hasEdge(g *roads.Graph, u, v uint32) bool {
	for e := g.First[u]; e < g.First[u+1]; e++ {
		if g.To[e] == v {
			return true
		}
	}
	return false
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	cal := s.city.Cal
	center := s.city.Center()
	var policy string
	var b2b bool
	s.rt.Do(func() { policy, b2b = s.rt.eng.Policy() })
	writeJSON(w, http.StatusOK, map[string]any{
		"replayDay": cal.ReplayDay, "month": cal.Month, "tripsDay": cal.TripsDay, "tripsMonth": cal.TripsMonth,
		"center": [2]float64{center.Lng, center.Lat}, "bounds": s.bounds,
		"products": []map[string]any{
			{"id": "standard", "name": "Standard", "seats": 4, "multiplier": 1},
			{"id": "comfort", "name": "Comfort", "seats": 4, "multiplier": s.cfg.Pricing.ComfortMultiplier},
			{"id": "xl", "name": "XL", "seats": 6, "multiplier": s.cfg.Pricing.XLMultiplier},
		},
		"fare": cal.Fare, "driverPay": cal.DriverPay, "fees": cal.Fees, "real": cal.Real,
		"clock":  map[string]any{"now": s.rt.clock.Now(), "speed": s.rt.clock.Speed(), "dayOffset": DayOffset},
		"policy": policy, "backToBack": b2b, "opsControls": s.cfg.Server.OpsControls, "opsResetMin": s.cfg.Server.OpsResetMin,
		"graph":    map[string]int{"nodes": s.city.Graph.N(), "edges": s.city.Graph.M()},
		"surgeRes": s.cfg.Surge.Resolution, "frameRes": frameRes, "mapStyle": s.cfg.Server.MapStyle, "mapStyleLight": s.cfg.Server.MapStyleLight,
		"places": len(s.city.Places),
	})
}

func (s *Server) places(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var near *geo.Point
	if lat, err1 := strconv.ParseFloat(q.Get("lat"), 64); err1 == nil {
		if lng, err2 := strconv.ParseFloat(q.Get("lng"), 64); err2 == nil {
			near = &geo.Point{Lat: lat, Lng: lng}
		}
	}
	text := q.Get("q")
	if len(text) > 80 {
		text = text[:80]
	}
	writeJSON(w, http.StatusOK, map[string]any{"places": s.city.Search(text, near, 8)})
}

type quoteRequest struct {
	RiderID  string    `json:"riderId"`
	From     geo.Point `json:"from"`
	To       geo.Point `json:"to"`
	FromName string    `json:"fromName"`
	ToName   string    `json:"toName"`
}

func trackPoints(t *roads.Track) [][2]float64 {
	out := make([][2]float64, len(t.Pts))
	for i, p := range t.Pts {
		out[i] = [2]float64{round(p.Lng, 6), round(p.Lat, 6)}
	}
	return out
}

func (s *Server) quote(w http.ResponseWriter, r *http.Request) {
	var req quoteRequest
	if !decode(w, r, &req) {
		return
	}
	if !riderID.MatchString(req.RiderID) {
		writeError(w, http.StatusBadRequest, errors.New("bad rider id"))
		return
	}
	fromName, _ := s.city.Resolve(req.FromName, req.From)
	toName, _ := s.city.Resolve(req.ToName, req.To)
	var q engine.QuoteResult
	var err error
	var pts [][2]float64
	s.rt.Do(func() {
		if q, err = s.rt.eng.QuoteNamed(req.RiderID, req.From, req.To, fromName, toName); err == nil {
			s.rt.eng.FillETAs(&q)
			pts = trackPoints(s.city.Graph.Track(q.Path))
		}
	})
	if err != nil {
		reply(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"quote": q, "route": pts})
}

type rideRequest struct {
	RiderID string `json:"riderId"`
	Token   string `json:"token"`
	Ref     string `json:"ref"`
}

func (s *Server) request(w http.ResponseWriter, r *http.Request) {
	var req rideRequest
	if !decode(w, r, &req) {
		return
	}
	if !riderID.MatchString(req.RiderID) || len(req.Ref) > 64 {
		writeError(w, http.StatusBadRequest, errors.New("bad rider id or ref"))
		return
	}
	var v engine.TripView
	var err error
	s.rt.Do(func() {
		v, err = s.rt.eng.Request(engine.RideRequest{RiderID: req.RiderID, Token: req.Token, Ref: req.Ref})
	})
	reply(w, v, err)
}

func (s *Server) trip(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var v engine.TripView
	var ok bool
	s.rt.Do(func() { v, ok = s.rt.eng.Trip(id) })
	if ok {
		writeJSON(w, http.StatusOK, v)
		return
	}
	if s.rt.store != nil {
		if doc, err := s.rt.store.Trip(r.Context(), id); err == nil && doc != nil {
			writeRaw(w, doc)
			return
		}
	}
	writeError(w, http.StatusNotFound, engine.ErrUnknownTrip)
}

func (s *Server) tripEvents(w http.ResponseWriter, r *http.Request) {
	if s.rt.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"events": []any{}, "persisted": false})
		return
	}
	evs, err := s.rt.store.Events(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": evs, "persisted": true})
}

type riderBody struct {
	RiderID string `json:"riderId"`
	Stars   int    `json:"stars"`
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	var b riderBody
	if !decode(w, r, &b) {
		return
	}
	var v engine.TripView
	var err error
	s.rt.Do(func() { v, err = s.rt.eng.Cancel(b.RiderID, r.PathValue("id")) })
	reply(w, v, err)
}

func (s *Server) rate(w http.ResponseWriter, r *http.Request) {
	var b riderBody
	if !decode(w, r, &b) {
		return
	}
	var err error
	s.rt.Do(func() { err = s.rt.eng.Rate(b.RiderID, r.PathValue("id"), b.Stars) })
	reply(w, map[string]bool{"ok": true}, err)
}

func (s *Server) active(w http.ResponseWriter, r *http.Request) {
	var v engine.TripView
	var ok bool
	s.rt.Do(func() { v, ok = s.rt.eng.ActiveTrip(r.PathValue("id")) })
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	if s.rt.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"trips": []any{}, "persisted": false})
		return
	}
	docs, err := s.rt.store.History(r.Context(), r.PathValue("id"), 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if docs == nil {
		docs = []json.RawMessage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"trips": docs, "persisted": true})
}

type onlineBody struct {
	Product string   `json:"product"`
	Lat     *float64 `json:"lat"`
	Lng     *float64 `json:"lng"`
}

func seedOf(id string) int64 {
	h := fnv.New64a()
	h.Write([]byte(id))
	return int64(h.Sum64() >> 1)
}

func (s *Server) online(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var b onlineBody
	if !driverID.MatchString(id) || !decode(w, r, &b) {
		if !driverID.MatchString(id) {
			writeError(w, http.StatusBadRequest, errors.New("bad driver id"))
		}
		return
	}
	product, err := engine.ParseProduct(b.Product)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var v engine.DriverView
	s.rt.Do(func() {
		if s.rt.eng.HumansOnline() >= s.cfg.Server.MaxHumanDrivers {
			err = errors.New("too many visitors are driving right now, try again in a minute")
			return
		}
		pos := s.rt.sim.SpawnPoint(s.rt.eng.Now())
		if b.Lat != nil && b.Lng != nil {
			pos = geo.Point{Lat: *b.Lat, Lng: *b.Lng}
		}
		v, err = s.rt.eng.DriverOnline(sim.HumanDriverInfo(seedOf(id), id, product), pos)
	})
	reply(w, v, err)
}

func (s *Server) offline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !driverID.MatchString(id) {
		writeError(w, http.StatusBadRequest, errors.New("bad driver id"))
		return
	}
	var err error
	var v engine.DriverView
	s.rt.Do(func() {
		if err = s.rt.eng.DriverOffline(id); err == nil {
			v, _ = s.rt.eng.Driver(id)
		}
	})
	reply(w, v, err)
}

type actionBody struct {
	OfferID string `json:"offerId"`
	TripID  string `json:"tripId"`
}

func (s *Server) offerAction(accept bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var b actionBody
		if !driverID.MatchString(id) || !decode(w, r, &b) {
			if !driverID.MatchString(id) {
				writeError(w, http.StatusBadRequest, errors.New("bad driver id"))
			}
			return
		}
		var err error
		var v engine.DriverView
		s.rt.Do(func() {
			if accept {
				err = s.rt.eng.Accept(id, b.OfferID)
			} else {
				err = s.rt.eng.Decline(id, b.OfferID)
			}
			v, _ = s.rt.eng.Driver(id)
		})
		reply(w, v, err)
	}
}

func (s *Server) tripAction(start bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var b actionBody
		if !driverID.MatchString(id) || !decode(w, r, &b) {
			if !driverID.MatchString(id) {
				writeError(w, http.StatusBadRequest, errors.New("bad driver id"))
			}
			return
		}
		var err error
		var v engine.DriverView
		s.rt.Do(func() {
			if start {
				err = s.rt.eng.StartTrip(id, b.TripID)
			} else {
				err = s.rt.eng.Complete(id, b.TripID)
			}
			v, _ = s.rt.eng.Driver(id)
		})
		reply(w, v, err)
	}
}

func (s *Server) driver(w http.ResponseWriter, r *http.Request) {
	var v engine.DriverView
	var ok bool
	s.rt.Do(func() { v, ok = s.rt.eng.Driver(r.PathValue("id")) })
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) driverRoute(w http.ResponseWriter, r *http.Request) {
	var tr *roads.Track
	var progress float64
	var ok bool
	s.rt.Do(func() { tr, progress, ok = s.rt.eng.DriverRoute(r.PathValue("id")) })
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	path := roads.Path{Nodes: tr.Nodes, Edges: tr.Edges, Meters: tr.Meters(), Seconds: tr.Duration()}
	writeJSON(w, http.StatusOK, map[string]any{
		"points": trackPoints(tr), "steps": s.city.Graph.Directions(path), "progress": progress,
		"meters": tr.Meters(), "seconds": tr.Duration(),
	})
}

func (s *Server) car(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.ParseUint(r.PathValue("num"), 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var c engine.DriverCard
	var ok bool
	s.rt.Do(func() { c, ok = s.rt.eng.DriverByNum(uint32(n)) })
	if !ok {
		writeError(w, http.StatusNotFound, engine.ErrUnknownDriver)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

type opsBody struct {
	Policy     *string  `json:"policy"`
	BackToBack *bool    `json:"backToBack"`
	Speed      *float64 `json:"speed"`
}

func (s *Server) ops(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Server.OpsControls {
		writeError(w, http.StatusForbidden, errors.New("ops controls are turned off on this server"))
		return
	}
	var b opsBody
	if !decode(w, r, &b) {
		return
	}
	if b.Speed != nil && (*b.Speed < 0.25 || *b.Speed > 10) {
		writeError(w, http.StatusBadRequest, errors.New("speed must be between 0.25 and 10"))
		return
	}
	var err error
	s.rt.Do(func() {
		policy, b2b := s.rt.eng.Policy()
		if b.Policy != nil {
			policy = *b.Policy
		}
		if b.BackToBack != nil {
			b2b = *b.BackToBack
		}
		if err = s.rt.eng.SetPolicy(policy, b2b); err == nil {
			s.rt.touchOps()
		}
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if b.Speed != nil {
		s.rt.SetSpeed(*b.Speed)
	}
	s.rt.publishStatsAsync()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var rl role
	id := q.Get("id")
	switch q.Get("role") {
	case "ops":
		rl = roleOps
	case "rider":
		rl = roleRider
		if !riderID.MatchString(id) {
			writeError(w, http.StatusBadRequest, errors.New("bad rider id"))
			return
		}
	case "driver":
		rl = roleDriver
		if !driverID.MatchString(id) {
			writeError(w, http.StatusBadRequest, errors.New("bad driver id"))
			return
		}
	default:
		writeError(w, http.StatusBadRequest, errors.New("role must be ops, rider or driver"))
		return
	}
	hub := s.rt.hub
	if hub.Count() >= maxClients {
		writeError(w, http.StatusServiceUnavailable, errors.New("too many viewers right now"))
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	ctx := r.Context()
	c := hub.add(conn, rl, id)
	go c.writer(ctx)
	c.enqueue(mustJSON(map[string]any{"t": "hello", "now": s.rt.clock.Now(), "speed": s.rt.clock.Speed(), "replayDay": s.city.Cal.ReplayDay}))
	c.enqueue(s.rt.SurgeJSON())
	switch rl {
	case roleOps:
		c.enqueue(s.rt.StatsJSON())
	case roleDriver:
		s.rt.Do(func() {
			if v, ok := s.rt.eng.Driver(id); ok {
				c.enqueue(mustJSON(map[string]any{"t": "driver", "driver": v}))
				if tr, _, ok := s.rt.eng.DriverRoute(id); ok && v.Trip != nil {
					c.enqueue(mustJSON(map[string]any{"t": "route", "route": s.resume(id, v.Trip, tr)}))
				}
			}
		})
	case roleRider:
		s.rt.Do(func() {
			if v, ok := s.rt.eng.ActiveTrip(id); ok {
				c.enqueue(mustJSON(map[string]any{"t": "trip", "trip": v, "event": "resume"}))
				if v.Driver != nil {
					if tr, _, ok := s.rt.eng.DriverRoute(v.Driver.ID); ok {
						c.enqueue(mustJSON(map[string]any{"t": "route", "route": s.resume(v.Driver.ID, &v, tr)}))
					}
				}
			}
		})
	}
	c.reader(ctx, s.bounds)
	if last := hub.remove(c); last && rl == roleDriver {
		time.AfterFunc(60*time.Second, func() {
			if !hub.HasDriver(id) {
				s.rt.Do(func() { _ = s.rt.eng.DriverOffline(id) })
			}
		})
	}
}

func (s *Server) resume(driverID string, trip *engine.TripView, tr *roads.Track) RouteView {
	purpose := "pickup"
	if trip.State == engine.Riding {
		purpose = "dropoff"
	}
	path := roads.Path{Nodes: tr.Nodes, Edges: tr.Edges, Meters: tr.Meters(), Seconds: tr.Duration()}
	return routeView(s.city.Graph, &engine.Route{DriverID: driverID, Purpose: purpose, Path: path, Track: tr}, trip)
}

type packed struct {
	raw  []byte
	gz   []byte
	kind string
}

func (s *Server) spa() http.Handler {
	dir := s.cfg.Server.WebDir
	var mu sync.Mutex
	cache := map[string]*packed{}
	load := func(name string) (*packed, error) {
		mu.Lock()
		defer mu.Unlock()
		if p := cache[name]; p != nil {
			return p, nil
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		zw.Write(raw)
		zw.Close()
		kind := mime.TypeByExtension(filepath.Ext(name))
		if kind == "" {
			kind = http.DetectContentType(raw)
		}
		p := &packed{raw: raw, gz: buf.Bytes(), kind: kind}
		cache[name] = p
		return p, nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Clean("/" + r.URL.Path)
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.IsDir() {
			name = "/index.html"
		}
		p, err := load(name)
		if err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte("The web app is not built yet. Run npm install and npm run build in web/, then restart.\n"))
			return
		}
		if strings.HasPrefix(name, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.Header().Set("Content-Type", p.kind)
		w.Header().Set("Vary", "Accept-Encoding")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") && len(p.gz) < len(p.raw) {
			w.Header().Set("Content-Encoding", "gzip")
			w.Write(p.gz)
			return
		}
		w.Write(p.raw)
	})
}
