package city

import (
	"strings"
	"sync"
	"testing"

	"github.com/adwitiyashukla/uber-replica/internal/geo"
)

var (
	once    sync.Once
	loaded  *City
	loadErr error
)

func manhattan(t testing.TB) *City {
	once.Do(func() { loaded, loadErr = Load("../../data") })
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	return loaded
}

func TestLoad(t *testing.T) {
	c := manhattan(t)
	if c.Graph.N() < 1000 || len(c.Demand) < 100000 || len(c.Places) < 5000 || len(c.Zones) < 60 {
		t.Fatalf("data looks incomplete: %d nodes, %d requests, %d places, %d zones", c.Graph.N(), len(c.Demand), len(c.Places), len(c.Zones))
	}
	if c.Cal.TripsDay != len(c.Demand) {
		t.Fatalf("calibration says %d trips but demand has %d", c.Cal.TripsDay, len(c.Demand))
	}
	for _, z := range c.Graph.Zones() {
		if _, ok := c.Zone(z); !ok {
			t.Fatalf("graph node zone %d has no zone record", z)
		}
	}
}

func TestHourFactorIsSmooth(t *testing.T) {
	c := manhattan(t)
	prev := c.HourFactor(0)
	for s := 60.0; s < 86400; s += 60 {
		f := c.HourFactor(s)
		if f < 0.5 || f > 4 {
			t.Fatalf("factor %v at %v s is out of range", f, s)
		}
		if d := f - prev; d > 0.05 || d < -0.05 {
			t.Fatalf("factor jumps by %v at %v s", d, s)
		}
		prev = f
	}
}

func TestTokens(t *testing.T) {
	cases := map[string]string{
		"West 42nd Street & 8th Avenue": "w 42 st 8 ave",
		"Grand Central Terminal":        "grand central terminal",
		"E 86th St and Lexington":       "e 86 st lexington",
	}
	for in, want := range cases {
		if got := strings.Join(Tokens(in), " "); got != want {
			t.Fatalf("Tokens(%q) = %q want %q", in, got, want)
		}
	}
}

func TestSearch(t *testing.T) {
	c := manhattan(t)
	near := geo.Point{Lat: 40.75, Lng: -73.98}
	for _, q := range []string{"grand central", "times sq", "42nd and 8th", "columbia univ"} {
		hits := c.Search(q, &near, 5)
		if len(hits) == 0 {
			t.Fatalf("no results for %q", q)
		}
		first := strings.Join(Tokens(hits[0].Name), " ")
		for _, tok := range Tokens(q)[:len(Tokens(q))-1] {
			if !strings.Contains(first, tok) {
				t.Fatalf("top hit %q for %q misses %q", hits[0].Name, q, tok)
			}
		}
	}
	if hits := c.Search("zzzz qqqq", nil, 5); len(hits) != 0 {
		t.Fatalf("nonsense query matched %v", hits)
	}
}

func FuzzSearch(f *testing.F) {
	c := manhattan(f)
	for _, s := range []string{"", "times", "42nd & 8th", "  ", "\x00\xff", "Cafe"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, q string) {
		for _, tok := range Tokens(q) {
			if tok == "" || tok != strings.ToLower(tok) {
				t.Fatalf("bad token %q from %q", tok, q)
			}
		}
		if hits := c.Search(q, nil, 8); len(hits) > 8 {
			t.Fatalf("limit ignored")
		}
	})
}
