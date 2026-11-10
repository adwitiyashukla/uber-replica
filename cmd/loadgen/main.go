package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/adwitiyashukla/uber-replica/internal/city"
	"github.com/adwitiyashukla/uber-replica/internal/config"
	"github.com/adwitiyashukla/uber-replica/internal/engine"
	"github.com/adwitiyashukla/uber-replica/internal/server"
)

func main() {
	cfgPath := flag.String("config", "config.toml", "config file")
	at := flag.String("at", "18:00", "replay clock time to start at")
	speed := flag.Float64("speed", 5, "simulation speed during the test")
	viewers := flag.Int("viewers", 100, "websocket viewers watching the whole city")
	quoters := flag.Int("quoters", 16, "concurrent clients asking for fare quotes")
	settle := flag.Duration("settle", 5*time.Second, "time to run before measuring")
	duration := flag.Duration("duration", 30*time.Second, "measured time")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	cfg.Clock.Mode, cfg.Clock.Start, cfg.Clock.Speed = "fixed", *at, *speed
	cfg.Server.RatePerSecond, cfg.Server.RateBurst = 1e6, 1e6
	cfg.Store.URL = ""
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}
	c, err := city.Load(cfg.Server.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := server.NewRuntime(cfg, c, []byte("load"), nil, "-l-")
	srv, err := server.New(cfg, rt, c)
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	go rt.Run(ctx)
	go http.Serve(ln, srv.Handler())
	base := "http://" + ln.Addr().String()
	fmt.Printf("server on %s, replay clock %s at %.0fx, settling for %s\n", base, *at, *speed, *settle)
	time.Sleep(*settle)
	rt.ResetLoopTimes()

	b := c.Bounds()
	bbox := []float64{b[0], b[1], b[2], b[3]}
	var frames, frameBytes, dialErrors atomic.Int64
	var wg sync.WaitGroup
	testCtx, stop := context.WithTimeout(ctx, *duration)
	defer stop()
	for i := 0; i < *viewers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, _, err := websocket.Dial(testCtx, "ws"+base[4:]+"/ws?role=ops", nil)
			if err != nil {
				dialErrors.Add(1)
				return
			}
			defer conn.CloseNow()
			conn.SetReadLimit(1 << 24)
			msg, _ := json.Marshal(map[string]any{"t": "view", "bbox": bbox})
			conn.Write(testCtx, websocket.MessageText, msg)
			for {
				typ, data, err := conn.Read(testCtx)
				if err != nil {
					return
				}
				if typ == websocket.MessageBinary {
					frames.Add(1)
					frameBytes.Add(int64(len(data)))
				}
			}
		}()
	}

	var mu sync.Mutex
	var latencies []float64
	var failures atomic.Int64
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: *quoters * 2}}
	g := c.Graph
	for i := 0; i < *quoters; i++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			rider := fmt.Sprintf("R-load%06d", seed)
			for testCtx.Err() == nil {
				a, d := g.Point(uint32(rng.Intn(g.N()))), g.Point(uint32(rng.Intn(g.N())))
				body, _ := json.Marshal(map[string]any{"riderId": rider, "from": a, "to": d})
				t0 := time.Now()
				res, err := client.Post(base+"/api/quote", "application/json", bytes.NewReader(body))
				if err != nil {
					if testCtx.Err() == nil {
						failures.Add(1)
					}
					continue
				}
				var out struct {
					Quote engine.QuoteResult `json:"quote"`
				}
				json.NewDecoder(res.Body).Decode(&out)
				res.Body.Close()
				ms := float64(time.Since(t0).Microseconds()) / 1000
				if res.StatusCode != 200 || len(out.Quote.Options) != 3 {
					failures.Add(1)
					continue
				}
				mu.Lock()
				latencies = append(latencies, ms)
				mu.Unlock()
			}
		}(int64(i + 1))
	}
	wg.Wait()
	secs := duration.Seconds()
	st := rt.Stats()
	p50, p99, worst := rt.LoopTimes()
	sort.Float64s(latencies)
	fmt.Println()
	fmt.Printf("cars online             %d (%d busy)\n", st.Online, st.Busy)
	fmt.Printf("viewers                 %d connected, %d failed to connect\n", *viewers-int(dialErrors.Load()), dialErrors.Load())
	fmt.Printf("position frames         %.2f per viewer per second, %.1f KB each\n",
		float64(frames.Load())/secs/float64(max(1, *viewers-int(dialErrors.Load()))), float64(frameBytes.Load())/float64(max(1, frames.Load()))/1024)
	fmt.Printf("websocket throughput    %.1f MB/s to viewers\n", float64(frameBytes.Load())/secs/1e6)
	fmt.Printf("quotes                  %.0f per second, %d rejected (same spot or no route)\n", float64(len(latencies))/secs, failures.Load())
	fmt.Printf("quote latency           p50 %.2f ms, p95 %.2f ms, p99 %.2f ms\n", engine.Percentile(latencies, 50), engine.Percentile(latencies, 95), engine.Percentile(latencies, 99))
	fmt.Printf("engine tick             p50 %.2f ms, p99 %.2f ms, worst %.2f ms (budget 100 ms)\n", p50, p99, worst)
	fmt.Printf("frame encode            %d us for the last frame\n", rt.Hub().Encode.Load())
}
