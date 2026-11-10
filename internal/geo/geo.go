package geo

import (
	"math"

	"github.com/uber/h3-go/v4"
)

const earthRadius = 6371008.8

type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

func rad(d float64) float64 { return d * math.Pi / 180 }

func Distance(a, b Point) float64 {
	p1, p2 := rad(a.Lat), rad(b.Lat)
	dp := p2 - p1
	dl := rad(b.Lng - a.Lng)
	h := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * earthRadius * math.Asin(math.Min(1, math.Sqrt(h)))
}

func Bearing(a, b Point) float64 {
	p1, p2 := rad(a.Lat), rad(b.Lat)
	dl := rad(b.Lng - a.Lng)
	y := math.Sin(dl) * math.Cos(p2)
	x := math.Cos(p1)*math.Sin(p2) - math.Sin(p1)*math.Cos(p2)*math.Cos(dl)
	return math.Mod(math.Atan2(y, x)*180/math.Pi+360, 360)
}

func Lerp(a, b Point, t float64) Point {
	return Point{Lat: a.Lat + (b.Lat-a.Lat)*t, Lng: a.Lng + (b.Lng-a.Lng)*t}
}

func TurnAngle(in, out float64) float64 {
	d := math.Mod(out-in+540, 360) - 180
	return d
}

func Cell(p Point, res int) h3.Cell {
	c, err := h3.LatLngToCell(h3.LatLng{Lat: p.Lat, Lng: p.Lng}, res)
	if err != nil {
		return 0
	}
	return c
}

func Rings(c h3.Cell, k int) [][]h3.Cell {
	r, err := h3.GridDiskDistances(c, k)
	if err != nil {
		return [][]h3.Cell{{c}}
	}
	return r
}

func Disk(c h3.Cell, k int) []h3.Cell {
	d, err := h3.GridDisk(c, k)
	if err != nil {
		return []h3.Cell{c}
	}
	return d
}

func Parent(c h3.Cell, res int) h3.Cell {
	p, err := c.Parent(res)
	if err != nil {
		return 0
	}
	return p
}

func Center(c h3.Cell) Point {
	ll, err := c.LatLng()
	if err != nil {
		return Point{}
	}
	return Point{Lat: ll.Lat, Lng: ll.Lng}
}

func Boundary(c h3.Cell) []Point {
	b, err := c.Boundary()
	if err != nil {
		return nil
	}
	out := make([]Point, len(b))
	for i, ll := range b {
		out[i] = Point{Lat: ll.Lat, Lng: ll.Lng}
	}
	return out
}

func CellsInBox(minLat, minLng, maxLat, maxLng float64, res int) []h3.Cell {
	loop := h3.GeoLoop{
		{Lat: minLat, Lng: minLng},
		{Lat: minLat, Lng: maxLng},
		{Lat: maxLat, Lng: maxLng},
		{Lat: maxLat, Lng: minLng},
	}
	cells, err := h3.PolygonToCellsExperimental(h3.GeoPolygon{GeoLoop: loop}, res, h3.ContainmentOverlapping)
	if err != nil {
		return nil
	}
	return cells
}
