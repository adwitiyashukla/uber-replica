package roads

import (
	"fmt"
	"math"
	"sort"

	"github.com/adwitiyashukla/uber-replica/internal/geo"
)

type Track struct {
	Pts   []geo.Point
	T     []float64
	M     []float64
	Nodes []uint32
	Edges []uint32
}

func (g *Graph) Track(p Path) *Track {
	t := &Track{Nodes: p.Nodes, Edges: p.Edges}
	if len(p.Edges) == 0 {
		t.Pts = []geo.Point{g.Point(p.Nodes[0])}
		t.T = []float64{0}
		t.M = []float64{0}
		return t
	}
	t.Pts = []geo.Point{g.Point(g.From[p.Edges[0]])}
	t.T = []float64{0}
	t.M = []float64{0}
	for _, e := range p.Edges {
		pts := g.EdgePoints(e)
		segs := make([]float64, len(pts)-1)
		total := 0.0
		for i := range segs {
			segs[i] = geo.Distance(pts[i], pts[i+1])
			total += segs[i]
		}
		et, em := float64(g.Time[e]), float64(g.Len[e])
		for i, s := range segs {
			frac := 1 / float64(len(segs))
			if total > 0 {
				frac = s / total
			}
			last := len(t.T) - 1
			t.Pts = append(t.Pts, pts[i+1])
			t.T = append(t.T, t.T[last]+et*frac)
			t.M = append(t.M, t.M[last]+em*frac)
		}
	}
	return t
}

func (t *Track) Duration() float64 { return t.T[len(t.T)-1] }

func (t *Track) Meters() float64 { return t.M[len(t.M)-1] }

func (t *Track) End() geo.Point { return t.Pts[len(t.Pts)-1] }

func (t *Track) At(sec float64) (geo.Point, float64) {
	n := len(t.Pts)
	if n == 1 {
		return t.Pts[0], 0
	}
	if sec <= 0 {
		return t.Pts[0], geo.Bearing(t.Pts[0], t.Pts[1])
	}
	if sec >= t.Duration() {
		return t.Pts[n-1], geo.Bearing(t.Pts[n-2], t.Pts[n-1])
	}
	i := sort.SearchFloat64s(t.T, sec)
	if i == 0 {
		i = 1
	}
	span := t.T[i] - t.T[i-1]
	frac := 0.0
	if span > 0 {
		frac = (sec - t.T[i-1]) / span
	}
	return geo.Lerp(t.Pts[i-1], t.Pts[i], frac), geo.Bearing(t.Pts[i-1], t.Pts[i])
}

func (t *Track) MetersAt(sec float64) float64 {
	if sec <= 0 {
		return 0
	}
	if sec >= t.Duration() {
		return t.Meters()
	}
	i := sort.SearchFloat64s(t.T, sec)
	if i == 0 {
		return 0
	}
	span := t.T[i] - t.T[i-1]
	if span <= 0 {
		return t.M[i]
	}
	return t.M[i-1] + (t.M[i]-t.M[i-1])*(sec-t.T[i-1])/span
}

func (t *Track) Project(p geo.Point, hint int) (float64, int, float64) {
	if len(t.Pts) == 1 {
		return 0, 0, geo.Distance(p, t.Pts[0])
	}
	seg, f, d := t.nearestSegment(p, max(0, hint-2), min(len(t.Pts)-1, hint+40))
	if d > 30 {
		seg, f, d = t.nearestSegment(p, 0, len(t.Pts)-1)
	}
	return t.T[seg] + (t.T[seg+1]-t.T[seg])*f, seg, d
}

func (t *Track) nearestSegment(p geo.Point, lo, hi int) (int, float64, float64) {
	k := math.Cos(p.Lat * math.Pi / 180)
	px, py := p.Lng*k, p.Lat
	bestSeg, bestF, bestD := lo, 0.0, math.Inf(1)
	for s := lo; s < hi; s++ {
		a, b := t.Pts[s], t.Pts[s+1]
		ax, ay := a.Lng*k, a.Lat
		dx, dy := b.Lng*k-ax, b.Lat-ay
		f := 0.0
		if l2 := dx*dx + dy*dy; l2 > 0 {
			f = min(1, max(0, ((px-ax)*dx+(py-ay)*dy)/l2))
		}
		ex, ey := ax+dx*f-px, ay+dy*f-py
		if d := ex*ex + ey*ey; d < bestD {
			bestSeg, bestF, bestD = s, f, d
		}
	}
	a, b := t.Pts[bestSeg], t.Pts[bestSeg+1]
	q := geo.Point{Lat: a.Lat + (b.Lat-a.Lat)*bestF, Lng: a.Lng + (b.Lng-a.Lng)*bestF}
	return bestSeg, bestF, geo.Distance(p, q)
}

type Step struct {
	Text   string  `json:"text"`
	Meters float64 `json:"meters"`
	Lat    float64 `json:"lat"`
	Lng    float64 `json:"lng"`
}

func compass(b float64) string {
	dirs := []string{"north", "northeast", "east", "southeast", "south", "southwest", "west", "northwest"}
	return dirs[int(math.Mod(b+22.5, 360)/45)%8]
}

func (g *Graph) Directions(p Path) []Step {
	if len(p.Edges) == 0 {
		return nil
	}
	type group struct {
		name   string
		first  uint32
		last   uint32
		meters float64
	}
	var groups []group
	for _, e := range p.Edges {
		name := g.Names[g.Name[e]]
		if len(groups) > 0 && (name == "" || name == groups[len(groups)-1].name) {
			groups[len(groups)-1].last = e
			groups[len(groups)-1].meters += float64(g.Len[e])
			continue
		}
		groups = append(groups, group{name: name, first: e, last: e, meters: float64(g.Len[e])})
	}
	steps := make([]Step, 0, len(groups)+1)
	for i, gr := range groups {
		pts := g.EdgePoints(gr.first)
		start := pts[0]
		label := gr.name
		if label == "" {
			label = "the road"
		}
		var text string
		if i == 0 {
			text = fmt.Sprintf("Head %s on %s", compass(geo.Bearing(pts[0], pts[1])), label)
		} else {
			prev := g.EdgePoints(groups[i-1].last)
			in := geo.Bearing(prev[len(prev)-2], prev[len(prev)-1])
			out := geo.Bearing(pts[0], pts[1])
			text = turnText(geo.TurnAngle(in, out), label)
		}
		steps = append(steps, Step{Text: text, Meters: gr.meters, Lat: start.Lat, Lng: start.Lng})
	}
	end := g.Point(p.Nodes[len(p.Nodes)-1])
	return append(steps, Step{Text: "Arrive at the destination", Lat: end.Lat, Lng: end.Lng})
}

func turnText(angle float64, name string) string {
	side := "right"
	if angle < 0 {
		side = "left"
	}
	a := math.Abs(angle)
	switch {
	case a < 25:
		return "Continue onto " + name
	case a < 60:
		return "Bear " + side + " onto " + name
	case a < 150:
		return "Turn " + side + " onto " + name
	default:
		return "Make a U-turn onto " + name
	}
}
