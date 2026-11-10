package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server   Server   `toml:"server"`
	Clock    Clock    `toml:"clock"`
	Dispatch Dispatch `toml:"dispatch"`
	Pricing  Pricing  `toml:"pricing"`
	Surge    Surge    `toml:"surge"`
	Sim      Sim      `toml:"sim"`
	Store    Store    `toml:"store"`
}

type Server struct {
	Addr            string  `toml:"addr"`
	DataDir         string  `toml:"data_dir"`
	WebDir          string  `toml:"web_dir"`
	OpsControls     bool    `toml:"ops_controls"`
	OpsResetMin     int     `toml:"ops_reset_min"`
	MaxHumanDrivers int     `toml:"max_human_drivers"`
	RatePerSecond   float64 `toml:"rate_per_second"`
	RateBurst       int     `toml:"rate_burst"`
	FrameMs         int     `toml:"frame_ms"`
	QuoteSecret     string  `toml:"quote_secret"`
	MapStyle        string  `toml:"map_style"`
	MapStyleLight   string  `toml:"map_style_light"`
}

type Clock struct {
	Mode      string  `toml:"mode"`
	Start     string  `toml:"start"`
	Speed     float64 `toml:"speed"`
	WarmupMin int     `toml:"warmup_min"`
}

type Dispatch struct {
	Policy            string  `toml:"policy"`
	BatchWindowMs     int     `toml:"batch_window_ms"`
	OfferTimeoutS     int     `toml:"offer_timeout_s"`
	MaxPickupS        int     `toml:"max_pickup_s"`
	SearchRings       int     `toml:"search_rings"`
	MaxCandidates     int     `toml:"max_candidates"`
	MaxBatch          int     `toml:"max_batch"`
	RequestTimeoutS   int     `toml:"request_timeout_s"`
	BackToBack        bool    `toml:"back_to_back"`
	B2BMaxRemainingS  int     `toml:"b2b_max_remaining_s"`
	CancelGraceS      int     `toml:"cancel_grace_s"`
	CancelFee         float64 `toml:"cancel_fee"`
	ArriveRadiusM     float64 `toml:"arrive_radius_m"`
	AgeWeight         float64 `toml:"age_weight"`
	DropoffDwellS     float64 `toml:"dropoff_dwell_s"`
	VisitorHeadStartS float64 `toml:"visitor_head_start_s"`
}

type Pricing struct {
	ComfortMultiplier float64 `toml:"comfort_multiplier"`
	XLMultiplier      float64 `toml:"xl_multiplier"`
	DriverSurgeShare  float64 `toml:"driver_surge_share"`
	QuoteTTLS         int     `toml:"quote_ttl_s"`
}

type Surge struct {
	Enabled     bool    `toml:"enabled"`
	Resolution  int     `toml:"resolution"`
	TickS       int     `toml:"tick_s"`
	HalfLifeS   int     `toml:"half_life_s"`
	TargetETAS  float64 `toml:"target_eta_s"`
	Sensitivity float64 `toml:"sensitivity"`
	Max         float64 `toml:"max"`
	StepUp      float64 `toml:"step_up"`
	StepDown    float64 `toml:"step_down"`
}

type Sim struct {
	Enabled           bool    `toml:"enabled"`
	Seed              int64   `toml:"seed"`
	DemandScale       float64 `toml:"demand_scale"`
	FleetBase         int     `toml:"fleet_base"`
	FleetScale        float64 `toml:"fleet_scale"`
	SupplyResponse    float64 `toml:"supply_response"`
	AcceptRate        float64 `toml:"accept_rate"`
	DecideMinS        float64 `toml:"decide_min_s"`
	DecideMaxS        float64 `toml:"decide_max_s"`
	PriceSensitivity  float64 `toml:"price_sensitivity"`
	PatienceMinS      float64 `toml:"patience_min_s"`
	PatienceMaxS      float64 `toml:"patience_max_s"`
	MaxAcceptableETAS float64 `toml:"max_acceptable_eta_s"`
	BoardMinS         float64 `toml:"board_min_s"`
	BoardMaxS         float64 `toml:"board_max_s"`
	DropoffS          float64 `toml:"dropoff_s"`
	RepositionAfterS  float64 `toml:"reposition_after_s"`
	RepositionRadiusM float64 `toml:"reposition_radius_m"`
	LocationEveryS    float64 `toml:"location_every_s"`
	ComfortShare      float64 `toml:"comfort_share"`
	XLShare           float64 `toml:"xl_share"`
	ComfortFleetShare float64 `toml:"comfort_fleet_share"`
	XLFleetShare      float64 `toml:"xl_fleet_share"`
	ManualTimeoutS    float64 `toml:"manual_timeout_s"`
}

type Store struct {
	URL            string `toml:"url"`
	FlushMs        int    `toml:"flush_ms"`
	RetentionHours int    `toml:"retention_hours"`
}

func Default() Config {
	return Config{
		Server: Server{
			Addr: ":8080", DataDir: "data", WebDir: "web/dist", OpsControls: true, OpsResetMin: 15,
			MaxHumanDrivers: 200, RatePerSecond: 5, RateBurst: 20, FrameMs: 1000,
			MapStyle: "https://tiles.openfreemap.org/styles/dark", MapStyleLight: "https://tiles.openfreemap.org/styles/positron",
		},
		Clock: Clock{Mode: "new_york", Start: "17:30", Speed: 1, WarmupMin: 90},
		Dispatch: Dispatch{
			Policy: "batch", BatchWindowMs: 2000, OfferTimeoutS: 15, MaxPickupS: 900, SearchRings: 10,
			MaxCandidates: 25, MaxBatch: 300, RequestTimeoutS: 600, BackToBack: true, B2BMaxRemainingS: 180,
			CancelGraceS: 120, CancelFee: 5, ArriveRadiusM: 250, AgeWeight: 0.2, DropoffDwellS: 15,
			VisitorHeadStartS: 300,
		},
		Pricing: Pricing{ComfortMultiplier: 1.3, XLMultiplier: 1.6, DriverSurgeShare: 0.5, QuoteTTLS: 180},
		Surge: Surge{
			Enabled: true, Resolution: 8, TickS: 10, HalfLifeS: 120, TargetETAS: 360,
			Sensitivity: 0.5, Max: 3, StepUp: 0.1, StepDown: 0.1,
		},
		Sim: Sim{
			Enabled: true, Seed: 7, DemandScale: 1, FleetBase: 600, FleetScale: 1, SupplyResponse: 0.5, AcceptRate: 0.92, DecideMinS: 2, DecideMaxS: 8,
			PriceSensitivity: 0.6, PatienceMinS: 240, PatienceMaxS: 720, MaxAcceptableETAS: 900,
			BoardMinS: 20, BoardMaxS: 60, DropoffS: 15, RepositionAfterS: 240, RepositionRadiusM: 2500,
			LocationEveryS: 1, ComfortShare: 0.12, XLShare: 0.08, ComfortFleetShare: 0.15, XLFleetShare: 0.1,
			ManualTimeoutS: 90,
		},
		Store: Store{FlushMs: 500, RetentionHours: 6},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		md, err := toml.DecodeFile(path, &cfg)
		if err != nil {
			return cfg, err
		}
		if extra := md.Undecoded(); len(extra) > 0 {
			keys := make([]string, len(extra))
			for i, k := range extra {
				keys[i] = k.String()
			}
			return cfg, fmt.Errorf("unknown keys in %s: %s", path, strings.Join(keys, ", "))
		}
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.Store.URL = v
	}
	if v := os.Getenv("QUOTE_SECRET"); v != "" {
		cfg.Server.QuoteSecret = v
	}
	if v := os.Getenv("PORT"); v != "" {
		cfg.Server.Addr = ":" + v
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	var errs []error
	check := func(ok bool, format string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}
	check(c.Server.Addr != "", "server.addr is empty")
	check(c.Server.MaxHumanDrivers >= 0, "server.max_human_drivers must be >= 0")
	check(c.Server.RatePerSecond > 0 && c.Server.RateBurst >= 1, "server.rate_per_second and rate_burst must be positive")
	check(c.Server.FrameMs >= 100 && c.Server.FrameMs <= 10000, "server.frame_ms must be in [100, 10000]")
	check(c.Clock.WarmupMin >= 0 && c.Clock.WarmupMin <= 240, "clock.warmup_min must be in [0, 240]")
	check(c.Server.OpsResetMin >= 0 && c.Server.OpsResetMin <= 1440, "server.ops_reset_min must be in [0, 1440]")
	check(c.Clock.Mode == "new_york" || c.Clock.Mode == "fixed", "clock.mode must be new_york or fixed")
	if _, err := ParseClock(c.Clock.Start); err != nil {
		errs = append(errs, err)
	}
	check(c.Clock.Speed > 0 && c.Clock.Speed <= 60, "clock.speed must be in (0, 60]")
	d := c.Dispatch
	check(d.Policy == "batch" || d.Policy == "greedy", "dispatch.policy must be batch or greedy")
	check(d.BatchWindowMs >= 100 && d.BatchWindowMs <= 30000, "dispatch.batch_window_ms must be in [100, 30000]")
	check(d.OfferTimeoutS >= 3 && d.OfferTimeoutS <= 120, "dispatch.offer_timeout_s must be in [3, 120]")
	check(d.MaxPickupS >= 60, "dispatch.max_pickup_s must be >= 60")
	check(d.SearchRings >= 1 && d.SearchRings <= 30, "dispatch.search_rings must be in [1, 30]")
	check(d.MaxCandidates >= 1 && d.MaxCandidates <= 200, "dispatch.max_candidates must be in [1, 200]")
	check(d.MaxBatch >= 1, "dispatch.max_batch must be >= 1")
	check(d.RequestTimeoutS >= d.OfferTimeoutS, "dispatch.request_timeout_s must be >= offer_timeout_s")
	check(d.B2BMaxRemainingS >= 0, "dispatch.b2b_max_remaining_s must be >= 0")
	check(d.CancelGraceS >= 0 && d.CancelFee >= 0, "dispatch cancel settings must be >= 0")
	check(d.ArriveRadiusM > 0, "dispatch.arrive_radius_m must be positive")
	check(d.AgeWeight >= 0 && d.AgeWeight <= 5, "dispatch.age_weight must be in [0, 5]")
	check(d.DropoffDwellS >= 0, "dispatch.dropoff_dwell_s must be >= 0")
	check(d.VisitorHeadStartS >= 0 && d.VisitorHeadStartS <= float64(d.MaxPickupS), "dispatch.visitor_head_start_s must be between 0 and max_pickup_s")
	p := c.Pricing
	check(p.ComfortMultiplier >= 1 && p.XLMultiplier >= 1, "pricing multipliers must be >= 1")
	check(p.DriverSurgeShare >= 0 && p.DriverSurgeShare <= 1, "pricing.driver_surge_share must be in [0, 1]")
	check(p.QuoteTTLS >= 10, "pricing.quote_ttl_s must be >= 10")
	s := c.Surge
	check(s.Resolution >= 6 && s.Resolution <= 9, "surge.resolution must be in [6, 9]")
	check(s.TickS >= 1 && s.HalfLifeS >= 1, "surge.tick_s and half_life_s must be >= 1")
	check(s.TargetETAS > 0 && s.Sensitivity > 0, "surge.target_eta_s and sensitivity must be positive")
	check(s.Max >= 1 && s.Max <= 10, "surge.max must be in [1, 10]")
	check(s.StepUp > 0 && s.StepDown > 0, "surge steps must be positive")
	m := c.Sim
	check(m.DemandScale >= 0 && m.DemandScale <= 5, "sim.demand_scale must be in [0, 5]")
	check(m.FleetBase >= 0 && m.FleetBase <= 20000, "sim.fleet_base must be in [0, 20000]")
	check(m.FleetScale > 0 && m.FleetScale <= 5, "sim.fleet_scale must be in (0, 5]")
	check(m.SupplyResponse >= 0 && m.SupplyResponse <= 2, "sim.supply_response must be in [0, 2]")
	check(m.AcceptRate > 0 && m.AcceptRate <= 1, "sim.accept_rate must be in (0, 1]")
	check(m.DecideMinS >= 0 && m.DecideMaxS >= m.DecideMinS && m.DecideMaxS < float64(d.OfferTimeoutS), "sim decide window must fit inside the offer timeout")
	check(m.PriceSensitivity >= 0, "sim.price_sensitivity must be >= 0")
	check(m.PatienceMinS > 0 && m.PatienceMaxS >= m.PatienceMinS, "sim patience window is invalid")
	check(m.MaxAcceptableETAS > 0, "sim.max_acceptable_eta_s must be positive")
	check(m.BoardMinS >= 0 && m.BoardMaxS >= m.BoardMinS && m.DropoffS >= 0, "sim dwell times are invalid")
	check(m.RepositionAfterS > 0 && m.RepositionRadiusM > 0, "sim reposition settings must be positive")
	check(m.LocationEveryS > 0 && m.LocationEveryS <= 30, "sim.location_every_s must be in (0, 30]")
	check(m.ComfortShare >= 0 && m.XLShare >= 0 && m.ComfortShare+m.XLShare < 1, "sim product shares must sum below 1")
	check(m.ComfortFleetShare >= 0 && m.XLFleetShare >= 0 && m.ComfortFleetShare+m.XLFleetShare < 1, "sim fleet shares must sum below 1")
	check(m.ManualTimeoutS >= 10, "sim.manual_timeout_s must be >= 10")
	check(c.Store.FlushMs >= 50 && c.Store.RetentionHours >= 1, "store.flush_ms must be >= 50 and retention_hours >= 1")
	return errors.Join(errs...)
}

func ParseClock(s string) (time.Duration, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("clock.start %q is not HH:MM", s)
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}
