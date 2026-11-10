package roads

import (
	"math"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"github.com/adwitiyashukla/uber-replica/internal/geo"
)

var (
	loadOnce  sync.Once
	manhattan *Graph
	loadErr   error
)

func realGraph(t testing.TB) *Graph {
	loadOnce.Do(func() { manhattan, loadErr = Load("../../data/graph.bin.gz") })
	if loadErr != nil {
		t.Fatalf("load graph: %v", loadErr)
	}
	return manhattan
}

func randomGraph(rng *rand.Rand, n, m int) *Graph {
	type edge struct {
		u, v uint32
		t    float32
	}
	var edges []edge
	for i := 0; i < n; i++ {
		edges = append(edges, edge{uint32(i), uint32((i + 1) % n), float32(1 + rng.Intn(50))})
	}
	for len(edges) < m {
		u, v := uint32(rng.Intn(n)), uint32(rng.Intn(n))
		if u != v {
			edges = append(edges, edge{u, v, float32(1 + rng.Intn(50))})
		}
	}
	g := &Graph{Names: []string{""}}
	g.Lat = make([]float64, n)
	g.Lng = make([]float64, n)
	g.Zone = make([]uint16, n)
	for i := range g.Lat {
		g.Lat[i] = 40.75 + rng.Float64()*1e-6
		g.Lng[i] = -73.98 + rng.Float64()*1e-6
	}
	g.First = make([]uint32, n+1)
	for _, e := range edges {
		g.First[e.u+1]++
	}
	for i := 1; i <= n; i++ {
		g.First[i] += g.First[i-1]
	}
	fill := make([]uint32, n)
	copy(fill, g.First[:n])
	g.To = make([]uint32, len(edges))
	g.Time = make([]float32, len(edges))
	g.Len = make([]float32, len(edges))
	g.Name = make([]uint32, len(edges))
	g.Class = make([]uint8, len(edges))
	g.GeoFirst = make([]uint32, len(edges)+1)
	for _, e := range edges {
		i := fill[e.u]
		fill[e.u]++
		g.To[i], g.Time[i], g.Len[i] = e.v, e.t, e.t
	}
	g.index()
	return g
}

func bellmanFord(g *Graph, src uint32) []float64 {
	d := make([]float64, g.N())
	for i := range d {
		d[i] = math.Inf(1)
	}
	d[src] = 0
	for it := 0; it < g.N(); it++ {
		changed := false
		for e := range g.To {
			u, v := g.From[e], g.To[e]
			if nd := d[u] + float64(g.Time[e]); nd < d[v] {
				d[v] = nd
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return d
}

func TestDijkstraMatchesBellmanFord(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 30; trial++ {
		g := randomGraph(rng, 40, 160)
		r := NewRouter(g)
		src := uint32(rng.Intn(g.N()))
		want := bellmanFord(g, src)
		for dst := 0; dst < g.N(); dst++ {
			p, ok := r.Dijkstra(src, uint32(dst))
			if !ok {
				t.Fatalf("no path %d->%d", src, dst)
			}
			if math.Abs(p.Seconds-want[dst]) > 1e-6 {
				t.Fatalf("trial %d %d->%d got %v want %v", trial, src, dst, p.Seconds, want[dst])
			}
		}
	}
}

func TestTimesToMatchesForwardSearch(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	g := randomGraph(rng, 60, 300)
	r := NewRouter(g)
	for trial := 0; trial < 20; trial++ {
		dst := uint32(rng.Intn(g.N()))
		targets := []uint32{uint32(rng.Intn(g.N())), uint32(rng.Intn(g.N())), dst}
		got := r.TimesTo(dst, targets, math.Inf(1))
		for i, s := range targets {
			want := bellmanFord(g, s)[dst]
			if math.Abs(got[i]-want) > 1e-6 {
				t.Fatalf("time %d->%d got %v want %v", s, dst, got[i], want)
			}
		}
	}
}

func TestTimesToRespectsLimit(t *testing.T) {
	g := realGraph(t)
	r := NewRouter(g)
	targets := []uint32{0, 100, 2000, 4000}
	all := r.TimesTo(10, targets, math.Inf(1))
	capped := r.TimesTo(10, targets, 120)
	for i := range targets {
		if all[i] <= 120 && capped[i] != all[i] {
			t.Fatalf("target %d inside limit lost: %v vs %v", i, capped[i], all[i])
		}
		if all[i] > 120 && !math.IsInf(capped[i], 1) {
			t.Fatalf("target %d outside limit returned %v", i, capped[i])
		}
	}
}

func TestAStarMatchesDijkstraOnManhattan(t *testing.T) {
	g := realGraph(t)
	r := NewRouter(g)
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 200; i++ {
		a, b := uint32(rng.Intn(g.N())), uint32(rng.Intn(g.N()))
		pa, ok1 := r.Route(a, b)
		pd, ok2 := r.Dijkstra(a, b)
		if !ok1 || !ok2 {
			t.Fatalf("graph should be strongly connected, %d->%d unreachable", a, b)
		}
		if math.Abs(pa.Seconds-pd.Seconds) > 1e-3*math.Max(1, pd.Seconds) {
			t.Fatalf("A* %v vs Dijkstra %v for %d->%d", pa.Seconds, pd.Seconds, a, b)
		}
		if pa.Nodes[0] != a || pa.Nodes[len(pa.Nodes)-1] != b || len(pa.Nodes) != len(pa.Edges)+1 {
			t.Fatalf("malformed path")
		}
		for j, e := range pa.Edges {
			if g.From[e] != pa.Nodes[j] || g.To[e] != pa.Nodes[j+1] {
				t.Fatalf("path edges do not chain")
			}
		}
	}
}

func TestNearestMatchesLinearScan(t *testing.T) {
	g := realGraph(t)
	rng := rand.New(rand.NewSource(4))
	for i := 0; i < 300; i++ {
		p := geo.Point{Lat: 40.705 + rng.Float64()*0.17, Lng: -74.015 + rng.Float64()*0.1}
		if a, b := g.Nearest(p), g.NearestLinear(p); a != b {
			t.Fatalf("grid nearest %d but linear scan found %d", a, b)
		}
	}
}

func TestTrackAtAndProject(t *testing.T) {
	g := realGraph(t)
	r := NewRouter(g)
	p, _ := r.Route(5, 3000)
	tr := g.Track(p)
	if math.Abs(tr.Duration()-p.Seconds) > 1e-3 || math.Abs(tr.Meters()-p.Meters) > 1e-2 {
		t.Fatalf("track totals %v %v vs path %v %v", tr.Duration(), tr.Meters(), p.Seconds, p.Meters)
	}
	hint := 0
	for s := 0.0; s < tr.Duration(); s += tr.Duration() / 37 {
		pt, _ := tr.At(s)
		got, seg, off := tr.Project(pt, hint)
		hint = seg
		if off > 1 || math.Abs(got-s) > 1 {
			t.Fatalf("project(at(%v)) = %v off by %v m", s, got, off)
		}
	}
	if end, _ := tr.At(tr.Duration() + 5); geo.Distance(end, g.Point(3000)) > 1e-6 {
		t.Fatalf("track should end at the destination")
	}
}

func TestDirections(t *testing.T) {
	g := realGraph(t)
	r := NewRouter(g)
	p, _ := r.Route(12, 4100)
	steps := g.Directions(p)
	if len(steps) < 2 || !strings.HasPrefix(steps[0].Text, "Head ") || steps[len(steps)-1].Text != "Arrive at the destination" {
		t.Fatalf("unexpected steps %+v", steps)
	}
	total := 0.0
	for _, s := range steps {
		total += s.Meters
	}
	if math.Abs(total-p.Meters) > 1 {
		t.Fatalf("step meters %v vs path %v", total, p.Meters)
	}
}

func TestTurnText(t *testing.T) {
	cases := map[float64]string{10: "Continue onto X", 40: "Bear right onto X", -90: "Turn left onto X", 175: "Make a U-turn onto X"}
	for a, want := range cases {
		if got := turnText(a, "X"); got != want {
			t.Fatalf("angle %v: %q want %q", a, got, want)
		}
	}
}

func BenchmarkAStar(b *testing.B) {
	g := realGraph(b)
	r := NewRouter(g)
	rng := rand.New(rand.NewSource(5))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Route(uint32(rng.Intn(g.N())), uint32(rng.Intn(g.N())))
	}
}

func BenchmarkDijkstra(b *testing.B) {
	g := realGraph(b)
	r := NewRouter(g)
	rng := rand.New(rand.NewSource(5))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Dijkstra(uint32(rng.Intn(g.N())), uint32(rng.Intn(g.N())))
	}
}

func BenchmarkTimesTo25Drivers(b *testing.B) {
	g := realGraph(b)
	r := NewRouter(g)
	rng := rand.New(rand.NewSource(6))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst := uint32(rng.Intn(g.N()))
		near := g.Point(dst)
		targets := make([]uint32, 0, 25)
		for len(targets) < 25 {
			n := uint32(rng.Intn(g.N()))
			if geo.Distance(near, g.Point(n)) < 2000 {
				targets = append(targets, n)
			}
		}
		r.TimesTo(dst, targets, 900)
	}
}

func BenchmarkNearestGrid(b *testing.B) {
	g := realGraph(b)
	rng := rand.New(rand.NewSource(7))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Nearest(geo.Point{Lat: 40.71 + rng.Float64()*0.15, Lng: -74.01 + rng.Float64()*0.06})
	}
}

func BenchmarkNearestLinear(b *testing.B) {
	g := realGraph(b)
	rng := rand.New(rand.NewSource(7))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.NearestLinear(geo.Point{Lat: 40.71 + rng.Float64()*0.15, Lng: -74.01 + rng.Float64()*0.06})
	}
}

func TestCurbNodesSkipMotorways(t *testing.T) {
	g := realGraph(t)
	highway := 0
	for i := 0; i < g.N(); i++ {
		n := uint32(i)
		if g.Curb(n) {
			continue
		}
		highway++
		if g.Nearest(g.Point(n)) != n {
			t.Fatalf("node %d should be its own nearest node", n)
		}
		if c := g.NearestCurb(g.Point(n)); !g.Curb(c) {
			t.Fatalf("NearestCurb returned motorway node %d", c)
		}
	}
	if highway == 0 || highway > g.N()/10 {
		t.Fatalf("expected a few motorway only nodes, got %d", highway)
	}
	for _, z := range g.Zones() {
		nodes := g.ZoneNodes(z)
		if len(nodes) == 0 {
			t.Fatalf("zone %d has no nodes", z)
		}
		for _, n := range nodes {
			if !g.Curb(n) {
				t.Fatalf("zone %d offers motorway node %d for pickups", z, n)
			}
		}
	}
}
