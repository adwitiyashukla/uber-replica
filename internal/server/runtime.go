package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adwitiyashukla/uber-replica/internal/city"
	"github.com/adwitiyashukla/uber-replica/internal/config"
	"github.com/adwitiyashukla/uber-replica/internal/engine"
	"github.com/adwitiyashukla/uber-replica/internal/sim"
	"github.com/adwitiyashukla/uber-replica/internal/store"
)

const DayOffset = int64(86400000)

type Clock struct {
	mu       sync.Mutex
	baseSim  int64
	baseWall time.Time
	speed    float64
}

func NewClock(cfg config.Clock) *Clock {
	start, _ := config.ParseClock(cfg.Start)
	sim := DayOffset + start.Milliseconds()
	if cfg.Mode == "new_york" {
		if loc, err := time.LoadLocation("America/New_York"); err == nil {
			t := time.Now().In(loc)
			sim = DayOffset + int64(t.Hour()*3600+t.Minute()*60+t.Second())*1000
		}
	}
	return &Clock{baseSim: sim, baseWall: time.Now(), speed: cfg.Speed}
}

func (c *Clock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.baseSim + int64(float64(time.Since(c.baseWall).Milliseconds())*c.speed)
}

func (c *Clock) Speed() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.speed
}

func (c *Clock) SetSpeed(s float64) {
	now := c.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.baseSim, c.baseWall, c.speed = now, time.Now(), s
}

type Runtime struct {
	cfg   config.Config
	city  *city.City
	eng   *engine.Engine
	sim   *sim.Sim
	clock *Clock
	hub   *Hub
	store *store.Store
	cmds  chan func()
	feed  *feed
	stats atomic.Pointer[[]byte]
	surge atomic.Pointer[[]byte]
	pos   []engine.Position
	opsAt time.Time

	loopMu sync.Mutex
	loops  []float64
}

func NewRuntime(cfg config.Config, c *city.City, secret []byte, st *store.Store, prefix string) *Runtime {
	e := engine.New(cfg, c, secret)
	e.SetPrefix(prefix)
	r := &Runtime{
		cfg: cfg, city: c, eng: e, sim: sim.New(cfg, c, e), clock: NewClock(cfg.Clock),
		store: st, cmds: make(chan func(), 256),
	}
	r.hub = NewHub(r)
	r.feed = newFeed(c)
	r.warmUp(time.Duration(cfg.Clock.WarmupMin) * time.Minute)
	r.publishStats()
	r.publishSurge()
	return r
}

func (r *Runtime) warmUp(d time.Duration) {
	began := time.Now()
	end := r.clock.Now()
	t := end - d.Milliseconds()
	r.eng.Start(t)
	r.sim.Start(t)
	r.settle()
	for t += 1000; t < end; t += 1000 {
		r.eng.Advance(t)
		r.settle()
		r.sim.Step(t)
		r.settle()
	}
	if d > 0 {
		log.Printf("ran %v of the replay ahead of time in %v", d, time.Since(began).Round(time.Millisecond))
	}
}

func (r *Runtime) settle() {
	for _, ev := range r.eng.Drain() {
		r.sim.Observe(ev)
	}
}

func (r *Runtime) Do(f func()) {
	done := make(chan struct{})
	r.cmds <- func() {
		r.eng.Advance(r.clock.Now())
		f()
		close(done)
	}
	<-done
}

func (r *Runtime) Run(ctx context.Context) {
	go r.hub.Run(ctx)
	tick := time.NewTicker(100 * time.Millisecond)
	frame := time.NewTicker(time.Duration(r.cfg.Server.FrameMs) * time.Millisecond)
	slow := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	defer frame.Stop()
	defer slow.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-r.cmds:
			f()
			r.flush()
		case <-tick.C:
			began := time.Now()
			now := r.clock.Now()
			r.eng.Advance(now)
			r.flush()
			r.sim.Step(now)
			r.flush()
			r.recordLoop(time.Since(began))
		case <-frame.C:
			r.pos = r.eng.Positions(r.pos)
			r.hub.Frame(r.eng.Now(), r.pos)
			r.sendProgress()
		case <-slow.C:
			r.resetOps()
			r.publishStats()
			r.hub.Broadcast(mustJSON(map[string]any{"t": "feed", "items": r.feed.take()}), roleOps)
		}
	}
}

func (r *Runtime) flush() {
	evs := r.eng.Drain()
	surgeChanged := false
	for i := range evs {
		ev := &evs[i]
		r.sim.Observe(*ev)
		if r.store != nil {
			r.store.Enqueue(*ev)
		}
		r.feed.observe(ev)
		r.route(ev)
		if ev.Kind == engine.SurgeChanged {
			surgeChanged = true
		}
	}
	if surgeChanged {
		r.publishSurge()
	}
}

func (r *Runtime) route(ev *engine.Event) {
	switch ev.Kind {
	case engine.OfferSent:
		r.hub.ToDriver(ev.DriverID, mustJSON(map[string]any{"t": "offer", "offer": ev.Offer}))
	case engine.OfferClosed:
		r.hub.ToDriver(ev.DriverID, mustJSON(map[string]any{"t": "offer_closed", "id": ev.Offer.ID, "reason": ev.Reason}))
	case engine.DriverRouted:
		if !r.hub.HasDriver(ev.DriverID) && (ev.Trip == nil || !r.hub.HasRider(ev.Trip.RiderID)) {
			return
		}
		msg := mustJSON(map[string]any{"t": "route", "route": routeView(r.city.Graph, ev.Route, ev.Trip)})
		r.hub.ToDriver(ev.DriverID, msg)
		if ev.Trip != nil {
			r.hub.ToRider(ev.Trip.RiderID, msg)
		}
	case engine.SurgeChanged, engine.Posted:
	default:
		if ev.Trip != nil {
			msg := mustJSON(map[string]any{"t": "trip", "trip": ev.Trip, "event": ev.Kind.String()})
			r.hub.ToRider(ev.Trip.RiderID, msg)
			if ev.Trip.Driver != nil {
				r.hub.ToDriver(ev.Trip.Driver.ID, msg)
			}
			if ev.Kind == engine.TripReopened {
				r.hub.ToDriver(ev.DriverID, msg)
			}
		}
	}
	if ev.DriverID != "" && r.hub.HasDriver(ev.DriverID) {
		if v, ok := r.eng.Driver(ev.DriverID); ok {
			r.hub.ToDriver(ev.DriverID, mustJSON(map[string]any{"t": "driver", "driver": v}))
		}
	}
	if ev.Trip != nil && ev.Trip.Driver != nil && ev.Trip.Driver.ID != ev.DriverID && r.hub.HasDriver(ev.Trip.Driver.ID) {
		if v, ok := r.eng.Driver(ev.Trip.Driver.ID); ok {
			r.hub.ToDriver(ev.Trip.Driver.ID, mustJSON(map[string]any{"t": "driver", "driver": v}))
		}
	}
}

func (r *Runtime) sendProgress() {
	for _, rider := range r.hub.Riders() {
		if v, ok := r.eng.ActiveTrip(rider); ok && (v.State == engine.Accepted || v.State == engine.Riding) {
			r.hub.ToRider(rider, mustJSON(map[string]any{"t": "trip", "trip": v, "event": "progress"}))
		}
	}
	for _, id := range r.hub.Drivers() {
		if v, ok := r.eng.Driver(id); ok && v.State != engine.Offline {
			r.hub.ToDriver(id, mustJSON(map[string]any{"t": "driver", "driver": v}))
		}
	}
}

type StatsMessage struct {
	engine.StatsView
	Sim       sim.Counters  `json:"sim"`
	SimOnline int           `json:"simOnline"`
	Speed     float64       `json:"speed"`
	Clients   int           `json:"clients"`
	Bytes     int64         `json:"bytesSent"`
	Store     *store.Totals `json:"store,omitempty"`
	Dropped   int64         `json:"storeDropped"`
	Written   int64         `json:"storeWritten"`
}

func (r *Runtime) publishStats() {
	m := StatsMessage{
		StatsView: r.eng.StatsView(), Sim: r.sim.Counters, SimOnline: r.sim.OnlineDrivers(),
		Speed: r.clock.Speed(), Clients: r.hub.Count(), Bytes: r.hub.BytesSent(),
	}
	if r.store != nil {
		m.Dropped, m.Written = r.store.Dropped.Load(), r.store.Written.Load()
	}
	b := mustJSON(map[string]any{"t": "stats", "stats": m})
	r.stats.Store(&b)
	r.hub.Broadcast(b, roleOps)
}

func (r *Runtime) recordLoop(d time.Duration) {
	r.loopMu.Lock()
	defer r.loopMu.Unlock()
	r.loops = append(r.loops, float64(d.Microseconds())/1000)
	if len(r.loops) > 3000 {
		r.loops = append(r.loops[:0], r.loops[len(r.loops)-1500:]...)
	}
}

func (r *Runtime) LoopTimes() (p50, p99, worst float64) {
	r.loopMu.Lock()
	xs := append([]float64(nil), r.loops...)
	r.loopMu.Unlock()
	for _, x := range xs {
		worst = math.Max(worst, x)
	}
	return engine.Percentile(xs, 50), engine.Percentile(xs, 99), worst
}

func (r *Runtime) ResetLoopTimes() {
	r.loopMu.Lock()
	r.loops = r.loops[:0]
	r.loopMu.Unlock()
}

func (r *Runtime) Hub() *Hub { return r.hub }

func (r *Runtime) Stats() engine.StatsView {
	var v engine.StatsView
	r.Do(func() { v = r.eng.StatsView() })
	return v
}

func (r *Runtime) publishStatsAsync() {
	r.Do(r.publishStats)
}

func (r *Runtime) publishSurge() {
	b := mustJSON(map[string]any{"t": "surge", "cells": r.eng.Surge()})
	r.surge.Store(&b)
	r.hub.Broadcast(b, roleAll)
}

func (r *Runtime) StatsJSON() []byte {
	if p := r.stats.Load(); p != nil {
		return *p
	}
	return []byte("{}")
}

func (r *Runtime) SurgeJSON() []byte {
	if p := r.surge.Load(); p != nil {
		return *p
	}
	return []byte(`{"t":"surge","cells":[]}`)
}

func (r *Runtime) touchOps() { r.opsAt = time.Now() }

func (r *Runtime) resetOps() {
	limit := time.Duration(r.cfg.Server.OpsResetMin) * time.Minute
	if r.opsAt.IsZero() || limit == 0 || time.Since(r.opsAt) < limit {
		return
	}
	r.opsAt = time.Time{}
	_ = r.eng.SetPolicy(r.cfg.Dispatch.Policy, r.cfg.Dispatch.BackToBack)
	if r.clock.Speed() != r.cfg.Clock.Speed {
		r.SetSpeed(r.cfg.Clock.Speed)
	}
	log.Printf("ops controls went back to the defaults after %v", limit)
}

func (r *Runtime) SetSpeed(s float64) {
	r.clock.SetSpeed(s)
	r.hub.Broadcast(r.clockJSON(), roleAll)
}

func (r *Runtime) clockJSON() []byte {
	return mustJSON(map[string]any{"t": "clock", "now": r.clock.Now(), "speed": r.clock.Speed()})
}

func mustJSON(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		log.Printf("json: %v", err)
		return []byte("{}")
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
