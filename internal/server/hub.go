package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/uber/h3-go/v4"

	"github.com/adwitiyashukla/uber-replica/internal/engine"
	"github.com/adwitiyashukla/uber-replica/internal/geo"
)

type role uint8

const (
	roleOps role = 1 << iota
	roleRider
	roleDriver
	roleAll = roleOps | roleRider | roleDriver
)

const (
	frameRes    = 7
	recordBytes = 16
	headerBytes = 13
)

type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	role   role
	id     string
	send   chan []byte
	frame  atomic.Pointer[[]byte]
	notify chan struct{}
	cells  atomic.Pointer[map[h3.Cell]bool]
	done   chan struct{}
	once   sync.Once
}

type frameJob struct {
	at  int64
	pos []engine.Position
}

type Hub struct {
	r       *Runtime
	mu      sync.RWMutex
	clients map[*Client]bool
	riders  map[string][]*Client
	drivers map[string][]*Client
	frames  chan frameJob
	spare   chan []engine.Position
	bytes   atomic.Int64
	Encode  atomic.Int64
}

func NewHub(r *Runtime) *Hub {
	return &Hub{
		r: r, clients: map[*Client]bool{}, riders: map[string][]*Client{}, drivers: map[string][]*Client{},
		frames: make(chan frameJob, 1), spare: make(chan []engine.Position, 2),
	}
}

func (h *Hub) Frame(at int64, pos []engine.Position) {
	var buf []engine.Position
	select {
	case buf = <-h.spare:
	default:
	}
	buf = append(buf[:0], pos...)
	select {
	case h.frames <- frameJob{at, buf}:
	default:
	}
}

func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-h.frames:
			start := time.Now()
			h.encode(job)
			h.Encode.Store(time.Since(start).Microseconds())
			select {
			case h.spare <- job.pos:
			default:
			}
		}
	}
}

func EncodeGroups(pos []engine.Position) map[h3.Cell][]byte {
	groups := map[h3.Cell][]byte{}
	var rec [recordBytes]byte
	for _, p := range pos {
		c := geo.Cell(geo.Point{Lat: p.Lat, Lng: p.Lng}, frameRes)
		binary.LittleEndian.PutUint32(rec[0:], p.Num)
		binary.LittleEndian.PutUint32(rec[4:], uint32(int32(math.Round(p.Lat*1e6))))
		binary.LittleEndian.PutUint32(rec[8:], uint32(int32(math.Round(p.Lng*1e6))))
		binary.LittleEndian.PutUint16(rec[12:], uint16(math.Mod(p.Heading+360, 360)*100))
		rec[14] = byte(p.State)
		rec[15] = byte(p.Product)
		groups[c] = append(groups[c], rec[:]...)
	}
	return groups
}

func Assemble(at int64, groups map[h3.Cell][]byte, cells map[h3.Cell]bool) []byte {
	n := 0
	for c := range cells {
		n += len(groups[c])
	}
	msg := make([]byte, headerBytes, headerBytes+n)
	msg[0] = 1
	binary.LittleEndian.PutUint64(msg[1:], math.Float64bits(float64(at)))
	binary.LittleEndian.PutUint32(msg[9:], uint32(n/recordBytes))
	for c := range cells {
		msg = append(msg, groups[c]...)
	}
	return msg
}

func (h *Hub) encode(job frameJob) {
	groups := EncodeGroups(job.pos)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		cells := c.cells.Load()
		if cells == nil {
			continue
		}
		msg := Assemble(job.at, groups, *cells)
		c.frame.Store(&msg)
		select {
		case c.notify <- struct{}{}:
		default:
		}
	}
}

func (h *Hub) add(conn *websocket.Conn, r role, id string) *Client {
	c := &Client{hub: h, conn: conn, role: r, id: id, send: make(chan []byte, 128), notify: make(chan struct{}, 1), done: make(chan struct{})}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
	switch r {
	case roleRider:
		h.riders[id] = append(h.riders[id], c)
	case roleDriver:
		h.drivers[id] = append(h.drivers[id], c)
	}
	return c
}

func without(list []*Client, c *Client) []*Client {
	out := list[:0]
	for _, x := range list {
		if x != c {
			out = append(out, x)
		}
	}
	return out
}

func (h *Hub) remove(c *Client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
	last := false
	switch c.role {
	case roleRider:
		if h.riders[c.id] = without(h.riders[c.id], c); len(h.riders[c.id]) == 0 {
			delete(h.riders, c.id)
		}
	case roleDriver:
		if h.drivers[c.id] = without(h.drivers[c.id], c); len(h.drivers[c.id]) == 0 {
			delete(h.drivers, c.id)
			last = true
		}
	}
	return last
}

func (h *Hub) Broadcast(b []byte, roles role) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if c.role&roles != 0 {
			c.enqueue(b)
		}
	}
}

func (h *Hub) ToRider(id string, b []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.riders[id] {
		c.enqueue(b)
	}
}

func (h *Hub) ToDriver(id string, b []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.drivers[id] {
		c.enqueue(b)
	}
}

func (h *Hub) HasRider(id string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.riders[id]) > 0
}

func (h *Hub) HasDriver(id string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.drivers[id]) > 0
}

func (h *Hub) Riders() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.riders))
	for id := range h.riders {
		out = append(out, id)
	}
	return out
}

func (h *Hub) Drivers() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.drivers))
	for id := range h.drivers {
		out = append(out, id)
	}
	return out
}

func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) BytesSent() int64 { return h.bytes.Load() }

func (c *Client) enqueue(b []byte) {
	select {
	case c.send <- b:
	default:
		c.close()
	}
}

func (c *Client) close() {
	c.once.Do(func() {
		close(c.done)
		c.conn.CloseNow()
	})
}

func (c *Client) write(ctx context.Context, typ websocket.MessageType, b []byte) error {
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.conn.Write(wctx, typ, b); err != nil {
		return err
	}
	c.hub.bytes.Add(int64(len(b)))
	return nil
}

func (c *Client) writer(ctx context.Context) {
	defer c.close()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case b := <-c.send:
			if c.write(ctx, websocket.MessageText, b) != nil {
				return
			}
		case <-c.notify:
			if p := c.frame.Swap(nil); p != nil {
				if c.write(ctx, websocket.MessageBinary, *p) != nil {
					return
				}
			}
		}
	}
}

type clientMsg struct {
	T    string     `json:"t"`
	BBox [4]float64 `json:"bbox"`
}

func (c *Client) reader(ctx context.Context, bounds [4]float64) {
	defer c.close()
	c.conn.SetReadLimit(4096)
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		var m clientMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.T == "view" {
			c.subscribe(m.BBox, bounds)
		}
	}
}

func (c *Client) subscribe(b, bounds [4]float64) {
	pad := 0.02
	w := math.Max(b[0], bounds[0]-pad)
	s := math.Max(b[1], bounds[1]-pad)
	e := math.Min(b[2], bounds[2]+pad)
	n := math.Min(b[3], bounds[3]+pad)
	set := map[h3.Cell]bool{}
	if e > w && n > s {
		for _, cell := range geo.CellsInBox(s, w, n, e, frameRes) {
			set[cell] = true
		}
	}
	c.cells.Store(&set)
}
