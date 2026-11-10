package engine

import (
	"math"
	"sort"

	"github.com/uber/h3-go/v4"

	"github.com/adwitiyashukla/uber-replica/internal/config"
	"github.com/adwitiyashukla/uber-replica/internal/geo"
	"github.com/adwitiyashukla/uber-replica/internal/roads"
)

type surge struct {
	cfg       config.Surge
	cells     []h3.Cell
	pos       map[h3.Cell]int
	neighbors [][]int
	etaSum    []float64
	quotes    []float64
	eta       []float64
	rate      []float64
	value     []float64
	shown     []float64
}

func newSurge(cfg config.Surge, g *roads.Graph) *surge {
	s := &surge{cfg: cfg, pos: map[h3.Cell]int{}}
	seen := map[h3.Cell]bool{}
	for i := 0; i < g.N(); i++ {
		c := geo.Cell(g.Point(uint32(i)), cfg.Resolution)
		if !seen[c] {
			seen[c] = true
			s.cells = append(s.cells, c)
		}
	}
	sort.Slice(s.cells, func(i, j int) bool { return s.cells[i] < s.cells[j] })
	for i, c := range s.cells {
		s.pos[c] = i
	}
	s.neighbors = make([][]int, len(s.cells))
	for i, c := range s.cells {
		for _, n := range geo.Disk(c, 1) {
			if j, ok := s.pos[n]; ok {
				s.neighbors[i] = append(s.neighbors[i], j)
			}
		}
		sort.Ints(s.neighbors[i])
	}
	n := len(s.cells)
	s.etaSum = make([]float64, n)
	s.quotes = make([]float64, n)
	s.eta = make([]float64, n)
	s.rate = make([]float64, n)
	s.value = make([]float64, n)
	s.shown = make([]float64, n)
	for i := range s.value {
		s.value[i], s.shown[i] = 1, 1
	}
	return s
}

func (s *surge) index(p geo.Point) int {
	if i, ok := s.pos[geo.Cell(p, s.cfg.Resolution)]; ok {
		return i
	}
	return -1
}

func (s *surge) observe(p geo.Point, eta float64) {
	if i := s.index(p); i >= 0 {
		s.quotes[i]++
		s.etaSum[i] += eta
	}
}

func (s *surge) at(p geo.Point) float64 {
	if i := s.index(p); i >= 0 {
		return s.shown[i]
	}
	return 1
}

func (s *surge) tick(dt float64) []SurgeCell {
	alpha := 1 - math.Exp(-dt*math.Ln2/float64(s.cfg.HalfLifeS))
	for i := range s.cells {
		if s.quotes[i] > 0 {
			avg := s.etaSum[i] / s.quotes[i]
			if s.rate[i] == 0 {
				s.eta[i] = avg
			} else {
				s.eta[i] += alpha * (avg - s.eta[i])
			}
		}
		s.rate[i] += alpha * (s.quotes[i]/(dt/60) - s.rate[i])
		s.quotes[i], s.etaSum[i] = 0, 0
	}
	var changed []SurgeCell
	for i := range s.cells {
		num, den := 0.0, 0.0
		for _, j := range s.neighbors[i] {
			w := s.rate[j]
			if j == i {
				w *= 2
			}
			num += w * s.eta[j]
			den += w
		}
		target := 1.0
		if den > 0.05 {
			if eta := num / den; eta > s.cfg.TargetETAS {
				target = math.Min(s.cfg.Max, 1+s.cfg.Sensitivity*(eta-s.cfg.TargetETAS)/s.cfg.TargetETAS)
			}
		}
		v := s.value[i]
		if target > v {
			v = math.Min(target, v+s.cfg.StepUp)
		} else {
			v = math.Max(target, v-s.cfg.StepDown)
		}
		s.value[i] = v
		if shown := math.Round(v*10) / 10; shown != s.shown[i] {
			s.shown[i] = shown
			changed = append(changed, SurgeCell{Cell: s.cells[i].String(), Value: shown})
		}
	}
	return changed
}

func (s *surge) snapshot() []SurgeCell {
	var out []SurgeCell
	for i, c := range s.cells {
		if s.shown[i] > 1 {
			out = append(out, SurgeCell{Cell: c.String(), Value: s.shown[i]})
		}
	}
	return out
}

func (s *surge) summary() (int, float64, float64) {
	n, top, sum := 0, 1.0, 0.0
	for _, v := range s.shown {
		sum += v
		if v > 1 {
			n++
		}
		top = math.Max(top, v)
	}
	return n, top, sum / math.Max(float64(len(s.shown)), 1)
}

func (e *Engine) Surge() []SurgeCell { return e.surge.snapshot() }

func (e *Engine) SurgeAt(p geo.Point) float64 {
	if !e.cfgS.Enabled {
		return 1
	}
	return e.surge.at(p)
}
