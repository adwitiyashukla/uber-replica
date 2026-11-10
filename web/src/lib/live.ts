export type Msg = { t: string; [key: string]: unknown };

export interface Frame {
  at: number;
  count: number;
  num: Uint32Array;
  lat: Float64Array;
  lng: Float64Array;
  heading: Float32Array;
  state: Uint8Array;
  product: Uint8Array;
}

export function decodeFrame(buf: ArrayBuffer): Frame {
  const dv = new DataView(buf);
  const at = dv.getFloat64(1, true);
  const count = dv.getUint32(9, true);
  const f: Frame = {
    at,
    count,
    num: new Uint32Array(count),
    lat: new Float64Array(count),
    lng: new Float64Array(count),
    heading: new Float32Array(count),
    state: new Uint8Array(count),
    product: new Uint8Array(count),
  };
  for (let i = 0, o = 13; i < count; i++, o += 16) {
    f.num[i] = dv.getUint32(o, true);
    f.lat[i] = dv.getInt32(o + 4, true) / 1e6;
    f.lng[i] = dv.getInt32(o + 8, true) / 1e6;
    f.heading[i] = dv.getUint16(o + 12, true) / 100;
    f.state[i] = dv.getUint8(o + 14);
    f.product[i] = dv.getUint8(o + 15);
  }
  return f;
}

export class Channel {
  private ws?: WebSocket;
  private view?: number[];
  private closed = false;
  private retry = 0;
  private listeners = new Set<(m: Msg) => void>();
  private frameListeners = new Set<(f: Frame) => void>();
  readonly role: string;
  readonly id?: string;

  constructor(role: string, id?: string) {
    this.role = role;
    this.id = id;
    this.open();
  }

  private open() {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const q = new URLSearchParams({ role: this.role });
    if (this.id) q.set("id", this.id);
    const ws = new WebSocket(`${proto}://${location.host}/ws?${q}`);
    ws.binaryType = "arraybuffer";
    ws.onopen = () => {
      this.retry = 0;
      if (this.view) this.send({ t: "view", bbox: this.view });
      this.emit({ t: "open" });
    };
    ws.onmessage = (ev) => {
      if (typeof ev.data === "string") {
        try {
          this.emit(JSON.parse(ev.data));
        } catch {
          return;
        }
      } else {
        const f = decodeFrame(ev.data as ArrayBuffer);
        this.frameListeners.forEach((l) => l(f));
      }
    };
    ws.onclose = () => {
      this.emit({ t: "close" });
      if (!this.closed) setTimeout(() => this.open(), Math.min(10000, 500 * 2 ** this.retry++));
    };
    this.ws = ws;
  }

  private emit(m: Msg) {
    this.listeners.forEach((l) => l(m));
  }

  private sentAt = 0;
  private pending = 0;

  setView(bbox: number[]) {
    this.view = bbox;
    const wait = 400 - (performance.now() - this.sentAt);
    if (wait <= 0) {
      this.sentAt = performance.now();
      this.send({ t: "view", bbox });
      return;
    }
    if (!this.pending) {
      this.pending = window.setTimeout(() => {
        this.pending = 0;
        this.sentAt = performance.now();
        if (this.view) this.send({ t: "view", bbox: this.view });
      }, wait);
    }
  }

  send(m: Msg) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(m));
  }

  on(l: (m: Msg) => void) {
    this.listeners.add(l);
    return () => {
      this.listeners.delete(l);
    };
  }

  onFrame(l: (f: Frame) => void) {
    this.frameListeners.add(l);
    return () => {
      this.frameListeners.delete(l);
    };
  }

  close() {
    this.closed = true;
    this.ws?.close();
  }
}

type RGB = [number, number, number];

const dark: Record<"idle" | "pickup" | "trip" | "off", RGB> = {
  idle: [39, 164, 123],
  pickup: [199, 130, 28],
  trip: [71, 141, 230],
  off: [120, 130, 140],
};

const light: Record<"idle" | "pickup" | "trip" | "off", RGB> = {
  idle: [31, 138, 102],
  pickup: [181, 111, 16],
  trip: [47, 116, 207],
  off: [150, 160, 168],
};

export function stateColors(theme: "dark" | "light"): RGB[] {
  const p = theme === "dark" ? dark : light;
  return [p.off, p.idle, p.idle, p.pickup, p.pickup, p.trip];
}

export interface Trail {
  path: number[];
  timestamps: number[];
  color: RGB;
}

const trailLength = 28;

export class Fleet {
  count = 0;
  nums: Uint32Array = new Uint32Array(0);
  from: Float32Array = new Float32Array(0);
  to: Float32Array = new Float32Array(0);
  pos: Float32Array = new Float32Array(0);
  heading: Float32Array = new Float32Array(0);
  state: Uint8Array = new Uint8Array(0);
  product: Uint8Array = new Uint8Array(0);
  colors: Uint8Array = new Uint8Array(0);
  at = 0;
  base = 0;
  version = 0;
  trails: Trail[] = [];
  private index = new Map<number, number>();
  private history = new Map<number, { path: number[]; timestamps: number[] }>();
  private t0 = 0;
  private span = 1000;
  private last = 0;
  readonly keepTrails: boolean;
  private palette: RGB[];

  constructor(theme: "dark" | "light", keepTrails = false) {
    this.keepTrails = keepTrails;
    this.palette = stateColors(theme);
  }

  progress(now: number) {
    return Math.min(1, (now - this.t0) / this.span);
  }

  update(f: Frame) {
    const now = performance.now();
    if (this.last) this.span = Math.min(3000, Math.max(300, now - this.last));
    this.last = now;
    if (!this.base) this.base = f.at;
    const k = this.progress(now);
    const n = f.count;
    const from = new Float32Array(n * 2);
    const to = new Float32Array(n * 2);
    const colors = new Uint8Array(n * 4);
    const index = new Map<number, number>();
    for (let i = 0; i < n; i++) {
      const num = f.num[i];
      index.set(num, i);
      to[2 * i] = f.lng[i];
      to[2 * i + 1] = f.lat[i];
      const j = this.index.get(num);
      if (j !== undefined) {
        const fx = this.from[2 * j] + (this.to[2 * j] - this.from[2 * j]) * k;
        const fy = this.from[2 * j + 1] + (this.to[2 * j + 1] - this.from[2 * j + 1]) * k;
        const far = Math.abs(fx - f.lng[i]) > 0.01 || Math.abs(fy - f.lat[i]) > 0.01;
        from[2 * i] = far ? f.lng[i] : fx;
        from[2 * i + 1] = far ? f.lat[i] : fy;
      } else {
        from[2 * i] = f.lng[i];
        from[2 * i + 1] = f.lat[i];
      }
      const c = this.palette[f.state[i]] ?? this.palette[0];
      colors.set([c[0], c[1], c[2], 240], 4 * i);
    }
    this.count = n;
    this.nums = f.num;
    this.from = from;
    this.to = to;
    this.pos = new Float32Array(n * 2);
    this.heading = f.heading;
    this.state = f.state;
    this.product = f.product;
    this.colors = colors;
    this.index = index;
    this.at = f.at;
    this.t0 = now;
    this.version++;
    if (this.keepTrails) this.updateTrails(f);
  }

  private updateTrails(f: Frame) {
    const seen = new Set<number>();
    const t = (f.at - this.base) / 1000;
    for (let i = 0; i < f.count; i++) {
      const s = f.state[i];
      if (s !== 3 && s !== 5) continue;
      const num = f.num[i];
      seen.add(num);
      let h = this.history.get(num);
      if (!h) {
        h = { path: [], timestamps: [] };
        this.history.set(num, h);
      }
      h.path.push(f.lng[i], f.lat[i]);
      h.timestamps.push(t);
      if (h.timestamps.length > trailLength) {
        h.path.splice(0, 2);
        h.timestamps.shift();
      }
    }
    const trails: Trail[] = [];
    for (const [num, h] of this.history) {
      if (!seen.has(num)) {
        this.history.delete(num);
        continue;
      }
      if (h.timestamps.length < 2) continue;
      const i = this.index.get(num)!;
      const c = this.palette[this.state[i]];
      trails.push({ path: h.path.slice(), timestamps: h.timestamps.slice(), color: c });
    }
    this.trails = trails;
  }

  trailTime(now: number) {
    return (this.at - this.base) / 1000 + this.progress(now) * (this.span / 1000);
  }

  positions(now: number) {
    const k = this.progress(now);
    const { from, to, pos } = this;
    for (let i = 0; i < pos.length; i++) pos[i] = from[i] + (to[i] - from[i]) * k;
    return pos;
  }

  locate(num: number, now: number): [number, number] | null {
    const i = this.index.get(num);
    if (i === undefined) return null;
    const k = this.progress(now);
    return [
      this.from[2 * i] + (this.to[2 * i] - this.from[2 * i]) * k,
      this.from[2 * i + 1] + (this.to[2 * i + 1] - this.from[2 * i + 1]) * k,
    ];
  }

  headingOf(num: number) {
    const i = this.index.get(num);
    return i === undefined ? 0 : this.heading[i];
  }

  numAt(i: number) {
    return this.nums[i];
  }
}

export class SimClock {
  private sim = 0;
  private perf = 0;
  speed = 1;

  set(now: number, speed: number) {
    this.sim = now;
    this.perf = performance.now();
    this.speed = speed;
  }

  now() {
    if (!this.perf) return 0;
    return this.sim + (performance.now() - this.perf) * this.speed;
  }
}
