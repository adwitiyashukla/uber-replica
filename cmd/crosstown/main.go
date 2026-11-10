package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/adwitiyashukla/uber-replica/internal/city"
	"github.com/adwitiyashukla/uber-replica/internal/config"
	"github.com/adwitiyashukla/uber-replica/internal/server"
	"github.com/adwitiyashukla/uber-replica/internal/store"
)

func main() {
	cfgPath := flag.String("config", "config.toml", "config file")
	flag.Parse()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	t0 := time.Now()
	c, err := city.Load(cfg.Server.DataDir)
	if err != nil {
		log.Fatalf("data: %v", err)
	}
	log.Printf("loaded %d road nodes, %d edges, %d replay requests, %d places in %s",
		c.Graph.N(), c.Graph.M(), len(c.Demand), len(c.Places), time.Since(t0).Round(time.Millisecond))

	secret := []byte(cfg.Server.QuoteSecret)
	if len(secret) == 0 {
		secret = make([]byte, 32)
		rand.Read(secret)
	}
	bootBytes := make([]byte, 3)
	rand.Read(bootBytes)
	boot := hex.EncodeToString(bootBytes)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var st *store.Store
	if cfg.Store.URL != "" {
		origin, _ := time.ParseInLocation("2006-01-02", c.Cal.ReplayDay, nyc())
		origin = origin.Add(-time.Duration(server.DayOffset) * time.Millisecond)
		st, err = store.Open(ctx, cfg.Store.URL, boot, origin,
			time.Duration(cfg.Store.FlushMs)*time.Millisecond, time.Duration(cfg.Store.RetentionHours)*time.Hour)
		if err != nil {
			log.Printf("running without postgres: %v", err)
			st = nil
		} else {
			defer st.Close()
			go st.Run(ctx)
			log.Printf("postgres connected, writing the trip log")
		}
	} else {
		log.Printf("no database url set, trips are kept in memory only")
	}

	rt := server.NewRuntime(cfg, c, secret, st, "-"+boot+"-")
	srv, err := server.New(cfg, rt, c)
	if err != nil {
		log.Fatal(err)
	}
	go rt.Run(ctx)
	hs := &http.Server{Addr: cfg.Server.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(shut)
	}()
	log.Printf("crosstown is up on %s, replaying %s at %.1fx (%s policy)", cfg.Server.Addr, c.Cal.ReplayDay, cfg.Clock.Speed, cfg.Dispatch.Policy)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func nyc() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.UTC
	}
	return loc
}
