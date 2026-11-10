package roads

import (
	"math"
)

type Path struct {
	Nodes   []uint32
	Edges   []uint32
	Meters  float64
	Seconds float64
}

type item struct {
	node uint32
	key  float32
}

type minHeap []item

func (h *minHeap) push(it item) {
	*h = append(*h, it)
	s := *h
	i := len(s) - 1
	for i > 0 {
		p := (i - 1) / 2
		if s[p].key <= s[i].key {
			break
		}
		s[p], s[i] = s[i], s[p]
		i = p
	}
}

func (h *minHeap) pop() item {
	s := *h
	top := s[0]
	last := len(s) - 1
	s[0] = s[last]
	s = s[:last]
	i := 0
	for {
		l, r := 2*i+1, 2*i+2
		m := i
		if l < len(s) && s[l].key < s[m].key {
			m = l
		}
		if r < len(s) && s[r].key < s[m].key {
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

type Router struct {
	g       *Graph
	dist    []float32
	via     []int32
	gen     []uint32
	done    []uint32
	want    []uint32
	cur     uint32
	h       minHeap
	Settled int
}

func NewRouter(g *Graph) *Router {
	n := g.N()
	return &Router{
		g:    g,
		dist: make([]float32, n),
		via:  make([]int32, n),
		gen:  make([]uint32, n),
		done: make([]uint32, n),
		want: make([]uint32, n),
	}
}

func (r *Router) reset() {
	r.cur++
	if r.cur == 0 {
		clear(r.gen)
		clear(r.done)
		clear(r.want)
		r.cur = 1
	}
	r.h = r.h[:0]
	r.Settled = 0
}

func (r *Router) Route(src, dst uint32) (Path, bool) {
	if !r.search(src, dst, true) {
		return Path{}, false
	}
	return r.path(src, dst), true
}

func (r *Router) Dijkstra(src, dst uint32) (Path, bool) {
	if !r.search(src, dst, false) {
		return Path{}, false
	}
	return r.path(src, dst), true
}

func (r *Router) search(src, dst uint32, astar bool) bool {
	r.reset()
	g := r.g
	gx, gy := g.X[dst], g.Y[dst]
	inv := 0.995 / g.maxSpeed
	heuristic := func(n uint32) float32 {
		if !astar {
			return 0
		}
		return float32(math.Hypot(g.X[n]-gx, g.Y[n]-gy) * inv)
	}
	r.gen[src], r.dist[src], r.via[src] = r.cur, 0, -1
	r.h.push(item{src, heuristic(src)})
	for len(r.h) > 0 {
		u := r.h.pop().node
		if r.done[u] == r.cur {
			continue
		}
		r.done[u] = r.cur
		r.Settled++
		if u == dst {
			return true
		}
		du := r.dist[u]
		for e := g.First[u]; e < g.First[u+1]; e++ {
			v := g.To[e]
			if r.done[v] == r.cur {
				continue
			}
			nd := du + g.Time[e]
			if r.gen[v] != r.cur || nd < r.dist[v] {
				r.gen[v], r.dist[v], r.via[v] = r.cur, nd, int32(e)
				r.h.push(item{v, nd + heuristic(v)})
			}
		}
	}
	return false
}

func (r *Router) path(src, dst uint32) Path {
	var edges []uint32
	for v := dst; v != src; {
		e := uint32(r.via[v])
		edges = append(edges, e)
		v = r.g.From[e]
	}
	for i, j := 0, len(edges)-1; i < j; i, j = i+1, j-1 {
		edges[i], edges[j] = edges[j], edges[i]
	}
	p := Path{Nodes: make([]uint32, 0, len(edges)+1), Edges: edges}
	p.Nodes = append(p.Nodes, src)
	for _, e := range edges {
		p.Nodes = append(p.Nodes, r.g.To[e])
		p.Meters += float64(r.g.Len[e])
		p.Seconds += float64(r.g.Time[e])
	}
	return p
}

func (r *Router) TimesTo(dst uint32, targets []uint32, limit float64) []float64 {
	r.reset()
	g := r.g
	out := make([]float64, len(targets))
	remaining := 0
	for _, t := range targets {
		if r.want[t] != r.cur {
			r.want[t] = r.cur
			remaining++
		}
	}
	r.gen[dst], r.dist[dst] = r.cur, 0
	r.h.push(item{dst, 0})
	for len(r.h) > 0 && remaining > 0 {
		it := r.h.pop()
		u := it.node
		if r.done[u] == r.cur {
			continue
		}
		if float64(it.key) > limit {
			break
		}
		r.done[u] = r.cur
		r.Settled++
		if r.want[u] == r.cur {
			remaining--
		}
		du := r.dist[u]
		for i := g.rFirst[u]; i < g.rFirst[u+1]; i++ {
			e := g.rEdge[i]
			v := g.From[e]
			if r.done[v] == r.cur {
				continue
			}
			nd := du + g.Time[e]
			if r.gen[v] != r.cur || nd < r.dist[v] {
				r.gen[v], r.dist[v] = r.cur, nd
				r.h.push(item{v, nd})
			}
		}
	}
	for i, t := range targets {
		if r.done[t] == r.cur {
			out[i] = float64(r.dist[t])
		} else {
			out[i] = math.Inf(1)
		}
	}
	return out
}

func (g *Graph) PathZones(p Path) []uint16 {
	var out []uint16
	for _, n := range p.Nodes {
		z := g.Zone[n]
		dup := false
		for _, x := range out {
			if x == z {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, z)
		}
	}
	return out
}
