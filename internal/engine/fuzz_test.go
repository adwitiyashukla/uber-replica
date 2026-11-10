package engine

import (
	"errors"
	"testing"
)

func FuzzQuoteToken(f *testing.F) {
	e := newEngine(f, nil)
	q, err := e.Quote("R1", timesSquare, unionSquare)
	if err != nil {
		f.Fatal(err)
	}
	good := q.Options[0].Token
	f.Add(good)
	f.Add("")
	f.Add(".")
	f.Add("eyJ9.abc")
	f.Add(good[:len(good)-3])
	f.Fuzz(func(t *testing.T, token string) {
		c, err := e.verify(token, "R1")
		if token == good {
			if err != nil {
				t.Fatalf("valid token rejected: %v", err)
			}
			return
		}
		if err == nil {
			if c.R != "R1" {
				t.Fatalf("accepted a token for another rider")
			}
			return
		}
		if !errors.Is(err, ErrBadQuote) && !errors.Is(err, ErrQuoteExpired) {
			t.Fatalf("unexpected error %v", err)
		}
	})
}
