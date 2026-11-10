package city

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"github.com/adwitiyashukla/uber-replica/internal/geo"
)

type Place struct {
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`
	Lat    float64 `json:"lat"`
	Lng    float64 `json:"lng"`
	Weight int     `json:"-"`
	tokens []string
}

func (p Place) Point() geo.Point { return geo.Point{Lat: p.Lat, Lng: p.Lng} }

var tokenMap = map[string]string{
	"street": "st", "avenue": "ave", "av": "ave", "west": "w", "east": "e", "north": "n", "south": "s",
	"square": "sq", "place": "pl", "boulevard": "blvd", "drive": "dr", "road": "rd", "parkway": "pkwy",
	"highway": "hwy", "lane": "ln", "terrace": "ter", "plaza": "plz", "expressway": "expy", "and": "",
}

func Tokens(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := fields[:0]
	for _, f := range fields {
		if m, ok := tokenMap[f]; ok {
			if m == "" {
				continue
			}
			f = m
		}
		out = append(out, stripOrdinal(f))
	}
	return out
}

func stripOrdinal(s string) string {
	if len(s) < 3 {
		return s
	}
	suffix := s[len(s)-2:]
	if suffix != "st" && suffix != "nd" && suffix != "rd" && suffix != "th" {
		return s
	}
	for _, r := range s[:len(s)-2] {
		if r < '0' || r > '9' {
			return s
		}
	}
	return s[:len(s)-2]
}

func loadPlaces(path string) ([]Place, error) {
	zr, f, err := openGz(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	var rows [][]json.RawMessage
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, err
	}
	out := make([]Place, 0, len(rows))
	for i, r := range rows {
		if len(r) != 5 {
			return nil, fmt.Errorf("place %d has %d fields", i, len(r))
		}
		var p Place
		for j, dst := range []any{&p.Name, &p.Kind, &p.Lat, &p.Lng, &p.Weight} {
			if err := json.Unmarshal(r[j], dst); err != nil {
				return nil, fmt.Errorf("place %d: %w", i, err)
			}
		}
		p.tokens = Tokens(p.Name)
		out = append(out, p)
	}
	return out, nil
}

func (c *City) Search(q string, near *geo.Point, limit int) []Place {
	qt := Tokens(q)
	if len(qt) == 0 || limit <= 0 {
		return nil
	}
	type hit struct {
		i     int
		score float64
	}
	var hits []hit
	for i, p := range c.Places {
		score, ok := match(qt, p.tokens)
		if !ok {
			continue
		}
		score += float64(p.Weight)
		if near != nil {
			score -= geo.Distance(*near, p.Point()) / 2000
		}
		hits = append(hits, hit{i, score})
	}
	sort.Slice(hits, func(a, b int) bool {
		if hits[a].score != hits[b].score {
			return hits[a].score > hits[b].score
		}
		return hits[a].i < hits[b].i
	})
	out := make([]Place, 0, min(limit, len(hits)))
	for _, h := range hits {
		if len(out) == limit {
			break
		}
		if p := c.Places[h.i]; !repeats(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func (c *City) Resolve(name string, near geo.Point) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return "", false
	}
	hits := c.Search(name, &near, 8)
	for _, h := range hits {
		if strings.EqualFold(h.Name, name) && geo.Distance(h.Point(), near) < 400 {
			return h.Name, true
		}
	}
	for _, h := range hits {
		if geo.Distance(h.Point(), near) < 400 {
			return h.Name, true
		}
	}
	return "", false
}

func repeats(chosen []Place, p Place) bool {
	for _, q := range chosen {
		if strings.EqualFold(q.Name, p.Name) && geo.Distance(q.Point(), p.Point()) < 400 {
			return true
		}
	}
	return false
}

func match(query, tokens []string) (float64, bool) {
	bonus := 0.0
	for qi, q := range query {
		found := false
		for pi, t := range tokens {
			if t == q || (qi == len(query)-1 && strings.HasPrefix(t, q)) {
				found = true
				if qi == 0 && pi == 0 {
					bonus += 2
				}
				break
			}
		}
		if !found {
			return 0, false
		}
	}
	if len(tokens) == len(query) {
		bonus++
	}
	return bonus, true
}
