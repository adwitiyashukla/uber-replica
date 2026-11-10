package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/adwitiyashukla/uber-replica/internal/city"
	"github.com/adwitiyashukla/uber-replica/internal/config"
	"github.com/adwitiyashukla/uber-replica/internal/engine"
	"github.com/adwitiyashukla/uber-replica/internal/sim"
)

const day = int64(86400000)

func parseClock(s string) int64 {
	if s == "24:00" {
		return day
	}
	d, err := config.ParseClock(s)
	if err != nil {
		log.Fatal(err)
	}
	return d.Milliseconds()
}

func main() {
	cfgPath := flag.String("config", "config.toml", "config file")
	from := flag.String("from", "00:00", "start of the measured window, HH:MM")
	to := flag.String("to", "24:00", "end of the measured window, HH:MM")
	warmup := flag.Duration("warmup", 30*time.Minute, "simulated time before the window starts")
	runs := flag.String("runs", "batch+b2b,batch,greedy", "comma separated dispatch setups")
	fleet := flag.Float64("fleet", 0, "override sim.fleet_scale")
	demand := flag.Float64("demand", 0, "override sim.demand_scale")
	seed := flag.Int64("seed", 0, "override sim.seed")
	every := flag.Float64("ping", 4, "seconds between simulated GPS pings")
	surge := flag.Bool("surge", true, "turn surge pricing on or off")
	out := flag.String("json", "", "also write the summaries to this JSON file")
	quiet := flag.Bool("quiet", false, "skip hourly progress lines")
	hourly := flag.Bool("hourly", false, "also print each run hour by hour next to real Uber")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	c, err := city.Load(cfg.Server.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	cfg.Sim.Enabled = true
	cfg.Sim.LocationEveryS = *every
	cfg.Surge.Enabled = *surge
	if *fleet > 0 {
		cfg.Sim.FleetScale = *fleet
	}
	if *demand > 0 {
		cfg.Sim.DemandScale = *demand
	}
	if *seed != 0 {
		cfg.Sim.Seed = *seed
	}
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}
	start, end := day+parseClock(*from), day+parseClock(*to)
	if end <= start {
		log.Fatal("the window must end after it starts")
	}
	fmt.Printf("replaying %s %s with %d real requests in the day, fleet scale %.2f, demand scale %.2f\n",
		c.Cal.ReplayDay, sim.Window(start, end), len(c.Demand), cfg.Sim.FleetScale, cfg.Sim.DemandScale)
	var results []sim.Summary
	for _, spec := range strings.Split(*runs, ",") {
		run := cfg
		policy, b2b, _ := strings.Cut(strings.TrimSpace(spec), "+")
		run.Dispatch.Policy = policy
		run.Dispatch.BackToBack = b2b == "b2b"
		if err := run.Validate(); err != nil {
			log.Fatal(err)
		}
		t0 := time.Now()
		s := replay(run, c, start, end, warmup.Milliseconds(), *quiet)
		s.Policy, s.BackToBack, s.Window = policy, run.Dispatch.BackToBack, sim.Window(start, end)
		fmt.Printf("%s done in %s\n", spec, time.Since(t0).Round(time.Second))
		results = append(results, s)
	}
	fmt.Println()
	fmt.Print(sim.Table(results))
	for _, r := range results {
		if *hourly {
			name := r.Policy
			if r.BackToBack {
				name += "+b2b"
			}
			fmt.Printf("\n%s by hour\n%s", name, sim.HourTable(r))
		}
	}
	if *out != "" {
		b, _ := json.MarshalIndent(results, "", "  ")
		if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
			log.Fatal(err)
		}
	}
}

func replay(cfg config.Config, c *city.City, from, to, warmup int64, quiet bool) sim.Summary {
	begin := from - warmup
	e := engine.New(cfg, c, []byte("replay"))
	e.Start(begin)
	s := sim.New(cfg, c, e)
	m := sim.NewMetrics(from, to)
	feed := func() {
		for _, ev := range e.Drain() {
			s.Observe(ev)
			m.Observe(ev)
		}
	}
	s.Start(begin)
	feed()
	peak := 0
	stop := to + 40*60*1000
	for now := begin; now <= stop; now += 1000 {
		e.Advance(now)
		feed()
		s.Step(now)
		feed()
		peak = max(peak, s.OnlineDrivers())
		if !quiet && now%3600000 == 0 {
			st := e.StatsView()
			fmt.Printf("  %s online %5d busy %5.1f%% open %4d done %7d wait p50 %4.0fs surge cells %3d\n",
				sim.Window(now, now)[:5], st.Online, st.Utilization*100, st.Open, st.Totals.Completed, st.WaitP50, st.SurgeCells)
		}
	}
	return m.Summary(c, s, e.Utilization(), peak)
}
