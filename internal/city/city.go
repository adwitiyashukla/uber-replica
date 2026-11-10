package city

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/adwitiyashukla/uber-replica/internal/geo"
	"github.com/adwitiyashukla/uber-replica/internal/roads"
)

type Linear struct {
	Base      float64 `json:"base"`
	PerMile   float64 `json:"per_mile"`
	PerMinute float64 `json:"per_minute"`
	Min       float64 `json:"min"`
	Fit       float64 `json:"fit"`
}

type Fees struct {
	SalesTaxRate        float64 `json:"sales_tax_rate"`
	BlackCarFundRate    float64 `json:"black_car_fund_rate"`
	CongestionSurcharge float64 `json:"congestion_surcharge"`
	CBDFee              float64 `json:"cbd_fee"`
}

type RealHour struct {
	Hour     int     `json:"hour"`
	Requests int     `json:"requests"`
	WaitP50  float64 `json:"wait_p50"`
	WaitP90  float64 `json:"wait_p90"`
	TripP50  float64 `json:"trip_p50"`
	TripP90  float64 `json:"trip_p90"`
	FareP50  float64 `json:"fare_p50"`
}

type Real struct {
	WaitP50  float64    `json:"wait_p50"`
	WaitP90  float64    `json:"wait_p90"`
	WaitMean float64    `json:"wait_mean"`
	TripP50  float64    `json:"trip_p50"`
	TripP90  float64    `json:"trip_p90"`
	FareP50  float64    `json:"fare_p50"`
	FareMean float64    `json:"fare_mean"`
	PayMean  float64    `json:"pay_mean"`
	MilesP50 float64    `json:"miles_p50"`
	Hours    []RealHour `json:"hours"`
}

type Calibration struct {
	Month          string               `json:"month"`
	ReplayDay      string               `json:"replay_day"`
	TripsMonth     int                  `json:"trips_month"`
	TripsDay       int                  `json:"trips_day"`
	Fare           Linear               `json:"fare"`
	DriverPay      Linear               `json:"driver_pay"`
	Fees           Fees                 `json:"fees"`
	South96        []uint16             `json:"zones_south_of_96th"`
	CBD            []uint16             `json:"zones_cbd"`
	HourFactor     []float64            `json:"hour_factor"`
	DistanceRatio  []float64            `json:"distance_ratio"`
	Real           Real                 `json:"real"`
	ZoneForecast   map[string][]float64 `json:"zone_hour_forecast"`
	zoneForecastID map[uint16][]float64
}

type Zone struct {
	ID     uint16         `json:"id"`
	Name   string         `json:"name"`
	Lat    float64        `json:"lat"`
	Lng    float64        `json:"lng"`
	Served bool           `json:"served"`
	Rings  [][][2]float64 `json:"rings"`
}

type Request struct {
	T     int32
	PU    uint16
	DO    uint16
	Wait  int32
	Trip  int32
	Miles float32
	Fare  float32
	Pay   float32
}

type City struct {
	Graph   *roads.Graph
	Cal     Calibration
	Zones   []Zone
	Demand  []Request
	Places  []Place
	zoneIdx map[uint16]int
	south96 map[uint16]bool
	cbd     map[uint16]bool
}

func Load(dir string) (*City, error) {
	g, err := roads.Load(filepath.Join(dir, "graph.bin.gz"))
	if err != nil {
		return nil, fmt.Errorf("graph: %w", err)
	}
	c := &City{Graph: g, zoneIdx: map[uint16]int{}, south96: map[uint16]bool{}, cbd: map[uint16]bool{}}
	if err := readJSON(filepath.Join(dir, "calibration.json"), &c.Cal); err != nil {
		return nil, fmt.Errorf("calibration: %w", err)
	}
	if len(c.Cal.HourFactor) != 24 || len(c.Cal.Real.Hours) != 24 {
		return nil, errors.New("calibration must have 24 hourly values")
	}
	c.Cal.zoneForecastID = map[uint16][]float64{}
	for k, v := range c.Cal.ZoneForecast {
		id, err := strconv.Atoi(k)
		if err != nil || len(v) != 24 {
			return nil, fmt.Errorf("bad forecast row %q", k)
		}
		c.Cal.zoneForecastID[uint16(id)] = v
	}
	for _, z := range c.Cal.South96 {
		c.south96[z] = true
	}
	for _, z := range c.Cal.CBD {
		c.cbd[z] = true
	}
	if err := readJSON(filepath.Join(dir, "zones.json"), &c.Zones); err != nil {
		return nil, fmt.Errorf("zones: %w", err)
	}
	for i, z := range c.Zones {
		c.zoneIdx[z.ID] = i
	}
	if c.Demand, err = loadDemand(filepath.Join(dir, "demand.csv.gz")); err != nil {
		return nil, fmt.Errorf("demand: %w", err)
	}
	if c.Places, err = loadPlaces(filepath.Join(dir, "places.json.gz")); err != nil {
		return nil, fmt.Errorf("places: %w", err)
	}
	return c, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func openGz(path string) (io.ReadCloser, *os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return zr, f, nil
}

func loadDemand(path string) ([]Request, error) {
	zr, f, err := openGz(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(bufio.NewReader(zr))
	r.ReuseRecord = true
	if _, err := r.Read(); err != nil {
		return nil, err
	}
	var out []Request
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		var v [8]float64
		for i := range v {
			if v[i], err = strconv.ParseFloat(rec[i], 64); err != nil {
				return nil, fmt.Errorf("line %d: %w", len(out)+2, err)
			}
		}
		out = append(out, Request{
			T: int32(v[0]), PU: uint16(v[1]), DO: uint16(v[2]), Wait: int32(v[3]), Trip: int32(v[4]),
			Miles: float32(v[5]), Fare: float32(v[6]), Pay: float32(v[7]),
		})
	}
	for i := 1; i < len(out); i++ {
		if out[i].T < out[i-1].T {
			return nil, errors.New("demand rows are not sorted by time")
		}
	}
	return out, nil
}

func (c *City) Zone(id uint16) (Zone, bool) {
	i, ok := c.zoneIdx[id]
	if !ok {
		return Zone{}, false
	}
	return c.Zones[i], true
}

func (c *City) ZoneName(id uint16) string {
	if z, ok := c.Zone(id); ok {
		return z.Name
	}
	return "Manhattan"
}

func (c *City) South96(z uint16) bool { return c.south96[z] }

func (c *City) CBD(z uint16) bool { return c.cbd[z] }

func (c *City) Forecast(z uint16, hour int) float64 {
	row := c.Cal.zoneForecastID[z]
	if row == nil {
		return 0
	}
	return row[((hour%24)+24)%24]
}

func (c *City) TypicalRequests(hour int) float64 {
	sum := 0.0
	for _, row := range c.Cal.zoneForecastID {
		sum += row[((hour%24)+24)%24]
	}
	return sum
}

func (c *City) HourFactor(daySec float64) float64 {
	f := c.Cal.HourFactor
	x := math.Mod(daySec/3600-0.5+48, 24)
	h := int(x)
	frac := x - float64(h)
	return f[h] + (f[(h+1)%24]-f[h])*frac
}

func (c *City) ExpectedTrip(daySec float64) float64 {
	h := int(math.Mod(daySec/3600+24, 24))
	return c.Cal.Real.Hours[h].TripP50
}

func (c *City) Center() geo.Point {
	var p geo.Point
	n := float64(c.Graph.N())
	for i := 0; i < c.Graph.N(); i++ {
		p.Lat += c.Graph.Lat[i] / n
		p.Lng += c.Graph.Lng[i] / n
	}
	return p
}

func (c *City) Bounds() [4]float64 {
	b := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for i := 0; i < c.Graph.N(); i++ {
		b[0] = math.Min(b[0], c.Graph.Lng[i])
		b[1] = math.Min(b[1], c.Graph.Lat[i])
		b[2] = math.Max(b[2], c.Graph.Lng[i])
		b[3] = math.Max(b[3], c.Graph.Lat[i])
	}
	return b
}
