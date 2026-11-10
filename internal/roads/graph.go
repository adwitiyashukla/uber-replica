package roads

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"

	"github.com/adwitiyashukla/uber-replica/internal/geo"
)

type Graph struct {
	Lat, Lng     []float64
	Zone         []uint16
	First        []uint32
	To           []uint32
	From         []uint32
	Len          []float32
	Time         []float32
	Name         []uint32
	Class        []uint8
	GeoFirst     []uint32
	PtLat, PtLng []float64
	Names        []string

	X, Y      []float64
	rFirst    []uint32
	rEdge     []uint32
	maxSpeed  float64
	lat0      float64
	lng0      float64
	kx, ky    float64
	grid      [][]uint32
	gridW     int
	gridH     int
	minX      float64
	minY      float64
	zoneNodes map[uint16][]uint32
	zones     []uint16
	curb      []bool
}

const gridCell = 200.0

func Load(path string) (*Graph, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	return Read(bufio.NewReaderSize(zr, 1<<16))
}

func Read(r io.Reader) (*Graph, error) {
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, err
	}
	if string(magic) != "XTG1" {
		return nil, errors.New("not a graph file")
	}
	var hdr [4]uint32
	if err := binary.Read(r, binary.LittleEndian, &hdr); err != nil {
		return nil, err
	}
	n, m, np, nn := int(hdr[0]), int(hdr[1]), int(hdr[2]), int(hdr[3])
	g := &Graph{}
	var err error
	read := func(v any) {
		if err == nil {
			err = binary.Read(r, binary.LittleEndian, v)
		}
	}
	lat := make([]int32, n)
	lng := make([]int32, n)
	g.Zone = make([]uint16, n)
	g.First = make([]uint32, n+1)
	g.To = make([]uint32, m)
	g.Len = make([]float32, m)
	g.Time = make([]float32, m)
	g.Name = make([]uint32, m)
	g.Class = make([]uint8, m)
	g.GeoFirst = make([]uint32, m+1)
	plat := make([]int32, np)
	plng := make([]int32, np)
	read(lat)
	read(lng)
	read(g.Zone)
	read(g.First)
	read(g.To)
	read(g.Len)
	read(g.Time)
	read(g.Name)
	read(g.Class)
	read(g.GeoFirst)
	read(plat)
	read(plng)
	if err != nil {
		return nil, fmt.Errorf("graph body: %w", err)
	}
	g.Names = make([]string, nn)
	for i := range g.Names {
		var l uint16
		if err := binary.Read(r, binary.LittleEndian, &l); err != nil {
			return nil, err
		}
		b := make([]byte, l)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		g.Names[i] = string(b)
	}
	g.Lat, g.Lng = scale(lat), scale(lng)
	g.PtLat, g.PtLng = scale(plat), scale(plng)
	if err := g.check(); err != nil {
		return nil, err
	}
	g.index()
	return g, nil
}

func scale(v []int32) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = float64(x) / 1e6
	}
	return out
}

func (g *Graph) check() error {
	n, m := len(g.Lat), len(g.To)
	if int(g.First[n]) != m || int(g.GeoFirst[m]) != len(g.PtLat) {
		return errors.New("graph offsets do not match sizes")
	}
	for i := 0; i < n; i++ {
		if g.First[i] > g.First[i+1] {
			return errors.New("graph offsets are not sorted")
		}
	}
	for e, t := range g.To {
		if int(t) >= n || int(g.Name[e]) >= len(g.Names) || g.Time[e] <= 0 {
			return fmt.Errorf("bad edge %d", e)
		}
	}
	return nil
}

func (g *Graph) index() {
	n, m := len(g.Lat), len(g.To)
	g.From = make([]uint32, m)
	for u := 0; u < n; u++ {
		for e := g.First[u]; e < g.First[u+1]; e++ {
			g.From[e] = uint32(u)
		}
	}
	g.rFirst = make([]uint32, n+1)
	for _, v := range g.To {
		g.rFirst[v+1]++
	}
	for i := 1; i <= n; i++ {
		g.rFirst[i] += g.rFirst[i-1]
	}
	g.rEdge = make([]uint32, m)
	fill := make([]uint32, n)
	copy(fill, g.rFirst[:n])
	for e, v := range g.To {
		g.rEdge[fill[v]] = uint32(e)
		fill[v]++
	}
	for e := range g.To {
		if s := float64(g.Len[e]) / float64(g.Time[e]); s > g.maxSpeed {
			g.maxSpeed = s
		}
	}
	for i := 0; i < n; i++ {
		g.lat0 += g.Lat[i] / float64(n)
		g.lng0 += g.Lng[i] / float64(n)
	}
	g.kx = 111320 * math.Cos(g.lat0*math.Pi/180)
	g.ky = 110574
	g.X = make([]float64, n)
	g.Y = make([]float64, n)
	g.minX, g.minY = math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for i := 0; i < n; i++ {
		g.X[i], g.Y[i] = g.planar(g.Point(uint32(i)))
		g.minX, g.minY = math.Min(g.minX, g.X[i]), math.Min(g.minY, g.Y[i])
		maxX, maxY = math.Max(maxX, g.X[i]), math.Max(maxY, g.Y[i])
	}
	g.gridW = int((maxX-g.minX)/gridCell) + 1
	g.gridH = int((maxY-g.minY)/gridCell) + 1
	g.grid = make([][]uint32, g.gridW*g.gridH)
	g.curb = make([]bool, n)
	for e := range g.To {
		if g.Class[e] > 0 {
			g.curb[g.From[e]] = true
			g.curb[g.To[e]] = true
		}
	}
	g.zoneNodes = make(map[uint16][]uint32)
	all := make(map[uint16][]uint32)
	for i := 0; i < n; i++ {
		cx, cy := g.gridCell(g.X[i], g.Y[i])
		g.grid[cy*g.gridW+cx] = append(g.grid[cy*g.gridW+cx], uint32(i))
		z := g.Zone[i]
		if len(all[z]) == 0 {
			g.zones = append(g.zones, z)
		}
		all[z] = append(all[z], uint32(i))
		if g.curb[i] {
			g.zoneNodes[z] = append(g.zoneNodes[z], uint32(i))
		}
	}
	for z, nodes := range all {
		if len(g.zoneNodes[z]) == 0 {
			g.zoneNodes[z] = nodes
		}
	}
}

func (g *Graph) Curb(n uint32) bool { return g.curb[n] }

func (g *Graph) N() int { return len(g.Lat) }

func (g *Graph) M() int { return len(g.To) }

func (g *Graph) Point(n uint32) geo.Point { return geo.Point{Lat: g.Lat[n], Lng: g.Lng[n]} }

func (g *Graph) ZoneNodes(z uint16) []uint32 { return g.zoneNodes[z] }

func (g *Graph) Zones() []uint16 { return g.zones }

func (g *Graph) MaxSpeed() float64 { return g.maxSpeed }

func (g *Graph) planar(p geo.Point) (float64, float64) {
	return (p.Lng - g.lng0) * g.kx, (p.Lat - g.lat0) * g.ky
}

func (g *Graph) gridCell(x, y float64) (int, int) {
	cx := min(max(int((x-g.minX)/gridCell), 0), g.gridW-1)
	cy := min(max(int((y-g.minY)/gridCell), 0), g.gridH-1)
	return cx, cy
}

func (g *Graph) PlanarDistance(a, b uint32) float64 {
	return math.Hypot(g.X[a]-g.X[b], g.Y[a]-g.Y[b])
}

func (g *Graph) Nearest(p geo.Point) uint32 { return g.nearest(p, false) }

func (g *Graph) NearestCurb(p geo.Point) uint32 { return g.nearest(p, true) }

func (g *Graph) nearest(p geo.Point, curb bool) uint32 {
	px, py := g.planar(p)
	cx, cy := g.gridCell(px, py)
	best, bestD := uint32(0), math.Inf(1)
	limit := max(g.gridW, g.gridH)
	for r := 0; r <= limit; r++ {
		for y := cy - r; y <= cy+r; y++ {
			if y < 0 || y >= g.gridH {
				continue
			}
			for x := cx - r; x <= cx+r; x++ {
				if x < 0 || x >= g.gridW || (max(abs(x-cx), abs(y-cy)) != r) {
					continue
				}
				for _, n := range g.grid[y*g.gridW+x] {
					if curb && !g.curb[n] {
						continue
					}
					if d := math.Hypot(g.X[n]-px, g.Y[n]-py); d < bestD || (d == bestD && n < best) {
						best, bestD = n, d
					}
				}
			}
		}
		if bestD <= float64(r)*gridCell {
			break
		}
	}
	return best
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (g *Graph) NearestLinear(p geo.Point) uint32 {
	px, py := g.planar(p)
	best, bestD := uint32(0), math.Inf(1)
	for i := range g.X {
		if d := math.Hypot(g.X[i]-px, g.Y[i]-py); d < bestD {
			best, bestD = uint32(i), d
		}
	}
	return best
}

func (g *Graph) EdgePoints(e uint32) []geo.Point {
	pts := []geo.Point{g.Point(g.From[e])}
	for i := g.GeoFirst[e]; i < g.GeoFirst[e+1]; i++ {
		pts = append(pts, geo.Point{Lat: g.PtLat[i], Lng: g.PtLng[i]})
	}
	return append(pts, g.Point(g.To[e]))
}

func (g *Graph) NodeName(n uint32) string {
	type named struct {
		name  string
		class uint8
	}
	var seen []named
	add := func(e uint32) {
		nm := g.Names[g.Name[e]]
		if nm == "" {
			return
		}
		for i := range seen {
			if seen[i].name == nm {
				seen[i].class = min(seen[i].class, g.Class[e])
				return
			}
		}
		seen = append(seen, named{nm, g.Class[e]})
	}
	for e := g.First[n]; e < g.First[n+1]; e++ {
		add(e)
	}
	for i := g.rFirst[n]; i < g.rFirst[n+1]; i++ {
		add(g.rEdge[i])
	}
	sort.Slice(seen, func(i, j int) bool {
		if seen[i].class != seen[j].class {
			return seen[i].class < seen[j].class
		}
		return seen[i].name < seen[j].name
	})
	switch len(seen) {
	case 0:
		return "Unnamed road"
	case 1:
		return seen[0].name
	}
	return seen[0].name + " & " + seen[1].name
}
