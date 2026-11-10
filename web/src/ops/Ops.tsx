import { useMemo, useRef, useState } from "react";
import type { Layer } from "@deck.gl/core";
import { cellToLatLng } from "h3-js";
import { api } from "../lib/api";
import { clock, compact, minutes, money, pct, productName } from "../lib/format";
import type { Live } from "../hooks";
import type { FeedItem, Meta, Stats, SurgeCell } from "../lib/types";
import { CityMap } from "../map/CityMap";
import { carsLayer, pingsLayer, surgeLabels, surgeLayer, trailsLayer, type Ping } from "../map/layers";
import { Spark } from "../ui/kit";

const stateText = ["offline", "free", "holding a request", "heading to a pickup", "waiting at a pickup", "with a rider"];
const productIds = ["standard", "comfort", "xl"];

export function useOpsFeed() {
  const [stats, setStats] = useState<Stats | null>(null);
  const [feed, setFeed] = useState<FeedItem[]>([]);
  const pings = useRef<Ping[]>([]);
  const onMessage = (m: { t: string; [k: string]: unknown }) => {
    if (m.t === "stats") setStats(m.stats as Stats);
    if (m.t === "feed") {
      const items = (m.items as FeedItem[]) ?? [];
      if (items.length === 0) return;
      setFeed((f) => [...items.slice().reverse(), ...f].slice(0, 30));
      const now = performance.now();
      pings.current = [
        ...pings.current.filter((p) => now - p.born < 2400),
        ...items
          .filter((i) => i.lat && i.lng)
          .map((i, k) => ({
            at: [i.lng, i.lat] as [number, number],
            born: now + k * 120,
            color: (i.kind === "surge" ? [255, 77, 109] : i.human ? [255, 178, 63] : [234, 240, 242]) as [number, number, number],
          })),
      ];
    }
  };
  return { stats, feed, pings, onMessage };
}

export function OpsMap({
  meta,
  live,
  surge,
  pings,
  className,
  view,
}: {
  meta: Meta;
  live: Live;
  surge: SurgeCell[];
  pings: React.MutableRefObject<Ping[]>;
  className?: string;
  view: { center: [number, number]; zoom: number; padding: { top: number; bottom: number; left: number; right: number } };
}) {
  const centers = useMemo(() => {
    const m = new Map<string, [number, number]>();
    for (const c of surge) {
      const [lat, lng] = cellToLatLng(c.cell);
      m.set(c.cell, [lng, lat]);
    }
    return m;
  }, [surge]);
  return (
    <CityMap
      label="Live map of every car in the simulation"
      styleUrl={meta.mapStyle}
      theme="dark"
      center={view.center}
      zoom={view.zoom}
      minZoom={10.5}
      bearing={29}
      padding={view.padding}
      layers={(t, zoom) => {
        const now = performance.now();
        const out: Layer[] = [
          surgeLayer("ops-surge", surge, 1),
          trailsLayer(live.fleet, t),
          carsLayer("ops-cars", live.fleet, t, 16, 7),
          pingsLayer(pings.current.filter((p) => now - p.born < 2400 && now >= p.born), now),
        ];
        if (zoom >= 12.8) out.push(surgeLabels("ops-surge-labels", surge, centers, true));
        return out;
      }}
      tooltip={(layer, index) => {
        if (layer !== "ops-cars") return null;
        const f = live.fleet;
        return `${productName[productIds[f.product[index]]] ?? "Standard"} car ${f.numAt(index)}, ${stateText[f.state[index]]}`;
      }}
      onView={(b) => live.channel?.setView(b)}
      className={className}
    />
  );
}

export function Legend() {
  const items: [string, string][] = [
    ["#27a47b", "Free"],
    ["#c7821c", "Going to a pickup"],
    ["#478de6", "With a rider"],
  ];
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12px] text-mute">
      {items.map(([c, t]) => (
        <span key={t} className="flex items-center gap-1.5">
          <span className="inline-block h-2.5 w-2.5 rounded-full" style={{ background: c }} />
          {t}
        </span>
      ))}
      <span className="flex items-center gap-1.5">
        <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true">
          <path d="M6 0.8 10.6 3.4 10.6 8.6 6 11.2 1.4 8.6 1.4 3.4Z" fill="rgba(255,77,109,0.35)" stroke="#ff4d6d" />
        </svg>
        Busy area, higher prices
      </span>
    </div>
  );
}

function Readout({ label, value, note, title }: { label: string; value: string; note?: string; title: string }) {
  return (
    <div className="min-w-0" title={title}>
      <div className="text-[12px] text-mute">{label}</div>
      <div className="text-[22px] leading-tight font-bold">{value}</div>
      {note && <div className="truncate text-[12px] text-mute">{note}</div>}
    </div>
  );
}

export function Readouts({ stats, meta, simNow }: { stats: Stats; meta: Meta; simNow: number }) {
  const sec = (((simNow / 1000) % 86400) + 86400) % 86400;
  const hour = Math.floor(sec / 3600);
  const real = meta.real.hours[hour];
  const frac = (sec % 3600) / 3600;
  const trailing = meta.real.hours[(hour + 23) % 24].requests * (1 - frac) + real.requests * frac;
  return (
    <div className="grid grid-cols-3 gap-x-4 gap-y-3 sm:grid-cols-5 sm:gap-x-5">
      <Readout
        label="Cars online"
        value={stats.online.toLocaleString("en-US")}
        note={`${pct(stats.utilization)} busy`}
        title="Simulated drivers online, and the share heading to a pickup or carrying a rider"
      />
      <Readout label="Riders waiting" value={String(stats.open)} note={`${stats.offered} ${stats.offered === 1 ? "offer" : "offers"} out`} title="Requests that do not have a driver yet" />
      <Readout
        label="Median wait"
        value={stats.waitP50 ? minutes(stats.waitP50) : "-"}
        note={`Real Uber ${minutes(real.wait_p50)}`}
        title="Median time from request to the car arriving, last 30 minutes, next to the real Uber median for this hour of the replay day"
      />
      <Readout
        label="Trips, last hour"
        value={compact(stats.completed60)}
        note={`Real Uber ${compact(trailing)}`}
        title="Completed trips in the last 60 minutes, next to real Uber trips in the same 60 minutes of the replay day"
      />
      <Readout
        label="Busy areas"
        value={String(stats.surgeCells)}
        note={stats.surgeCells ? `Up to ${stats.surgeMax.toFixed(1)}x` : "Normal prices"}
        title="Hex areas where prices are raised because pickup times are long"
      />
    </div>
  );
}

export function Charts({ stats, meta, simNow }: { stats: Stats; meta: Meta; simNow: number }) {
  const series = stats.series ?? [];
  const hour = Math.floor((((simNow / 1000) % 86400) + 86400) % 86400 / 3600);
  const times = series.map((p) => clock(p.at));
  if (series.length < 3) return <p className="text-[13px] text-mute">The charts fill in after the first few minutes of the replay.</p>;
  return (
    <div className="flex gap-5">
      <Spark title="Requests per minute" values={series.map((p) => p.requests)} color="#478de6" format={(v) => String(Math.round(v))} times={times} />
      <Spark
        title="Median wait per minute"
        values={series.map((p) => p.waitP50 / 60)}
        reference={meta.real.hours[hour].wait_p50 / 60}
        referenceLabel="Real Uber, same hour"
        color="#c7821c"
        format={(v) => `${v.toFixed(1)} min`}
        times={times}
      />
    </div>
  );
}

export function Controls({ stats, meta }: { stats: Stats; meta: Meta }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  if (!meta.opsControls) return <p className="text-[12px] text-mute">Dispatch controls are turned off on this server.</p>;
  const send = async (body: { policy?: string; backToBack?: boolean; speed?: number }) => {
    setBusy(true);
    setError(null);
    try {
      await api.ops(body);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const seg = (on: boolean) =>
    `rounded-[9px] px-2.5 pt-[6px] pb-[4px] text-[13px] font-semibold transition-colors ${on ? "bg-ink text-night" : "text-mute hover:text-ink"}`;
  return (
    <div className="flex flex-wrap items-center gap-x-5 gap-y-2" aria-busy={busy}>
      <div className="flex items-center gap-2">
        <span className="text-[12px] text-mute">Dispatch</span>
        <div className="flex rounded-[11px] bg-raise p-[3px]" role="group" aria-label="Dispatch policy">
          <button type="button" className={seg(stats.policy === "batch")} aria-pressed={stats.policy === "batch"} onClick={() => send({ policy: "batch" })}>
            Batched
          </button>
          <button type="button" className={seg(stats.policy === "greedy")} aria-pressed={stats.policy === "greedy"} onClick={() => send({ policy: "greedy" })}>
            Nearest car
          </button>
        </div>
      </div>
      <label className="flex cursor-pointer items-center gap-2 text-[13px]">
        <input type="checkbox" className="h-4 w-4 accent-[#0d8556]" checked={stats.backToBack} onChange={(e) => send({ backToBack: e.target.checked })} />
        Back-to-back trips
      </label>
      <div className="flex items-center gap-2">
        <span className="text-[12px] text-mute">Speed</span>
        <div className="flex rounded-[11px] bg-raise p-[3px]" role="group" aria-label="Simulation speed">
          {[1, 2, 5, 10].map((s) => (
            <button key={s} type="button" className={seg(stats.speed === s)} aria-pressed={stats.speed === s} onClick={() => send({ speed: s })}>
              {s}x
            </button>
          ))}
        </div>
      </div>
      {meta.opsResetMin > 0 && <span className="text-[12px] text-mute">Settings reset {meta.opsResetMin} minutes after the last change.</span>}
      {error && <span className="text-[12px] text-surge">{error}</span>}
    </div>
  );
}

export function Feed({ items, limit }: { items: FeedItem[]; limit: number }) {
  return (
    <ol className="space-y-1.5" aria-live="polite">
      {items.slice(0, limit).map((i, k) => (
        <li key={`${i.at}-${k}-${i.text}`} className="flex gap-2 text-[13px] leading-snug">
          <span className="w-[62px] shrink-0 text-mute">{clock(i.at)}</span>
          <span className={i.human ? "text-amber" : "text-ink"}>{i.text}</span>
        </li>
      ))}
      {items.length === 0 && <li className="text-[13px] text-mute">Waiting for the first events.</li>}
    </ol>
  );
}

export function Totals({ stats }: { stats: Stats }) {
  const rows: [string, string][] = [
    ["Requests since start", stats.totals.requests.toLocaleString("en-US")],
    ["Completed", stats.totals.completed.toLocaleString("en-US")],
    ["No car found", stats.totals.noDriver.toLocaleString("en-US")],
    ["Rider cancels", stats.totals.riderCancel.toLocaleString("en-US")],
    ["Offers sent", stats.totals.offers.toLocaleString("en-US")],
    ["Offers declined", stats.totals.declined.toLocaleString("en-US")],
    ["Offers expired", stats.totals.expired.toLocaleString("en-US")],
    ["Riders lost to price", stats.sim.lost.toLocaleString("en-US")],
    ["Median time to match", stats.matchP50 ? `${Math.round(stats.matchP50)} s` : "-"],
    ["90th percentile wait", stats.waitP90 ? minutes(stats.waitP90) : "-"],
    ["Fares, last hour", money(stats.revenue60)],
    ["Driver pay, last hour", money(stats.driverPay60)],
    ["Trip log rows written", stats.storeWritten ? stats.storeWritten.toLocaleString("en-US") : "Postgres off"],
    ["Viewers connected", String(stats.clients)],
  ];
  return (
    <dl className="grid grid-cols-2 gap-x-4 gap-y-1.5 text-[13px]">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-mute">{k}</dt>
          <dd className="text-right font-semibold">{v}</dd>
        </div>
      ))}
    </dl>
  );
}
