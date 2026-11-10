import { useEffect, useState, type MouseEvent, type ReactNode } from "react";
import { useLive, useMeta, useNow, useWide } from "./hooks";
import { clock, day } from "./lib/format";
import type { Meta } from "./lib/types";
import { DriverApp } from "./driver/DriverApp";
import { RiderApp } from "./rider/RiderApp";
import { Charts, Controls, Feed, Legend, OpsMap, Readouts, Totals, useOpsFeed } from "./ops/Ops";
import { Phone, Plate } from "./ui/kit";

const repo = "https://github.com/adwitiyashukla/uber-replica";

type Go = (path: string) => (e: MouseEvent) => void;

function usePath(): [string, Go] {
  const [path, setPath] = useState(location.pathname);
  useEffect(() => {
    const on = () => setPath(location.pathname);
    window.addEventListener("popstate", on);
    return () => window.removeEventListener("popstate", on);
  }, []);
  const go: Go = (p) => (e) => {
    e.preventDefault();
    history.pushState(null, "", p);
    setPath(p);
  };
  return [path, go];
}

export function App() {
  const { meta, error } = useMeta();
  const [path, go] = usePath();
  if (!meta) {
    return (
      <main className="flex h-full flex-col items-center justify-center gap-4 bg-night">
        <Plate />
        <p className="text-[15px] text-mute">{error ?? "Loading Manhattan"}</p>
      </main>
    );
  }
  switch (path) {
    case "/rider":
      return <Solo meta={meta} go={go} kind="rider" />;
    case "/driver":
      return <Solo meta={meta} go={go} kind="driver" />;
    case "/ops":
      return <OpsPage meta={meta} go={go} />;
    default:
      return <ControlRoom meta={meta} go={go} />;
  }
}

function NavLink({ href, go, children }: { href: string; go: Go; children: ReactNode }) {
  return (
    <a href={href} onClick={go(href)} className="rounded-[10px] px-3 pt-[7px] pb-[5px] text-ink hover:bg-raise">
      {children}
    </a>
  );
}

function About({ meta, onClose }: { meta: Meta; onClose: () => void }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-night/70 p-4" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="about-title"
        className="rise max-h-full max-w-[560px] overflow-y-auto rounded-[22px] bg-panel p-6 text-[15px] leading-relaxed shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="about-title" className="text-[24px] font-extrabold leading-tight">
          How Crosstown works
        </h2>
        <p className="mt-3">
          Crosstown is a working ride hailing system I built to see how apps like Uber run behind the scenes. Every car on the map is a simulated driver. Every
          ride request is a real Uber trip from the NYC TLC trip records for {day(meta.replayDay)}, replayed at the minute it happened. That is{" "}
          {meta.tripsDay.toLocaleString("en-US")} trips inside Manhattan.
        </p>
        <p className="mt-3">
          The Go backend finds nearby cars with Uber's H3 hex grid, works out real road travel times on the OpenStreetMap street network, and matches riders to
          drivers every two seconds with the Hungarian algorithm. Fares come from a model fitted on {meta.tripsMonth.toLocaleString("en-US")} real trips. When pickup
          times climb in an area, its prices go up.
        </p>
        <p className="mt-3">
          You can join in. Request a ride on the rider phone, or go online on the driver phone and take real requests yourself. Your car drives itself, you
          make the calls.
        </p>
        <p className="mt-4 text-[13px] text-mute">
          Trip data from the NYC Taxi and Limousine Commission. Map data from OpenStreetMap contributors. Map tiles from OpenFreeMap.
        </p>
        <div className="mt-5 flex gap-2">
          <a href={repo} className="rounded-[12px] bg-sign px-4 pt-[11px] pb-[9px] font-bold text-white hover:bg-sign-hi">
            Read the code
          </a>
          <button type="button" onClick={onClose} className="rounded-[12px] bg-raise px-4 pt-[11px] pb-[9px] font-semibold">
            Close
          </button>
        </div>
      </div>
    </div>
  );
}

function Header({ meta, simNow, speed, go, onAbout, children }: { meta: Meta; simNow: number; speed: number; go: Go; onAbout: () => void; children?: ReactNode }) {
  return (
    <header className="pointer-events-none absolute inset-x-0 top-0 z-20 flex items-center gap-4 bg-gradient-to-b from-night via-night/80 to-night/0 px-5 pt-4 pb-10">
      <a href="/" onClick={go("/")} className="pointer-events-auto" aria-label="Crosstown home">
        <Plate />
      </a>
      <div className="pointer-events-auto text-[14px] leading-tight">
        <div className="font-semibold">
          Manhattan, {clock(simNow)}
          {speed !== 1 && <span className="ml-2 text-amber">{speed}x speed</span>}
        </div>
        <div className="text-mute">Live replay of {day(meta.replayDay)}</div>
      </div>
      <nav className="pointer-events-auto ml-auto flex items-center gap-1 text-[14px] font-semibold" aria-label="Views">
        {children}
        <button type="button" onClick={onAbout} className="rounded-[10px] px-3 pt-[7px] pb-[5px] hover:bg-raise">
          How it works
        </button>
        <a href={repo} className="rounded-[10px] px-3 pt-[7px] pb-[5px] hover:bg-raise">
          Code
        </a>
      </nav>
    </header>
  );
}

function ControlRoom({ meta, go }: { meta: Meta; go: Go }) {
  const wide = useWide(1180);
  if (!wide) return <MobileShell meta={meta} go={go} />;
  return <Desk meta={meta} go={go} />;
}

function Desk({ meta, go }: { meta: Meta; go: Go }) {
  const ops = useOpsFeed();
  const { live, surge } = useLive("ops", undefined, "dark", ops.onMessage);
  const [about, setAbout] = useState(false);
  useNow(1000);
  const simNow = live.clock.now() || meta.clock.now;
  const stats = ops.stats;
  return (
    <main className="relative h-full w-full overflow-hidden bg-night">
      <OpsMap
        meta={meta}
        live={live}
        surge={surge}
        pings={ops.pings}
        className="absolute inset-0"
        view={{ center: [-73.977, 40.757], zoom: 12.45, padding: { top: 190, bottom: 210, left: 390, right: 390 } }}
      />
      <Header meta={meta} simNow={simNow} speed={stats?.speed ?? meta.clock.speed} go={go} onAbout={() => setAbout(true)}>
        <NavLink href="/ops" go={go}>
          Ops view
        </NavLink>
      </Header>
      <div className="absolute top-[86px] bottom-5 left-5 z-10 w-[344px]">
        <Phone label="Rider app" framed>
          <RiderApp meta={meta} />
        </Phone>
      </div>
      <div className="absolute top-[86px] bottom-5 right-5 z-10 w-[344px]">
        <Phone label="Driver app" framed>
          <DriverApp meta={meta} />
        </Phone>
      </div>
      <section
        aria-label="Recent events"
        className="pointer-events-none absolute top-[92px] left-1/2 z-10 w-[min(520px,calc(100%-800px))] -translate-x-1/2 rounded-[16px] bg-panel/85 px-4 pt-3 pb-2.5 backdrop-blur"
      >
        <Feed items={ops.feed} limit={3} />
      </section>
      <section aria-label="Live operations" className="absolute right-[388px] bottom-5 left-[388px] z-10 rounded-[22px] bg-panel/92 p-4 shadow-2xl backdrop-blur">
        {stats ? (
          <>
            <Readouts stats={stats} meta={meta} simNow={simNow} />
            <div className="mt-3 flex flex-wrap items-center justify-between gap-3 border-t border-line pt-3">
              <Legend />
              <Controls stats={stats} meta={meta} />
            </div>
          </>
        ) : (
          <p className="text-[14px] text-mute">Connecting to the city...</p>
        )}
      </section>
      {about && <About meta={meta} onClose={() => setAbout(false)} />}
    </main>
  );
}

function OpsPage({ meta, go }: { meta: Meta; go: Go }) {
  const ops = useOpsFeed();
  const { live, surge } = useLive("ops", undefined, "dark", ops.onMessage);
  const [about, setAbout] = useState(false);
  useNow(1000);
  const simNow = live.clock.now() || meta.clock.now;
  const stats = ops.stats;
  const wide = useWide(900);
  return (
    <main className="relative flex h-full w-full overflow-hidden bg-night">
      <div className="relative min-w-0 flex-1">
        <OpsMap
          meta={meta}
          live={live}
          surge={surge}
          pings={ops.pings}
          className="absolute inset-0"
          view={{ center: [-73.969, 40.791], zoom: 11.2, padding: { top: 60, bottom: 10, left: 0, right: 0 } }}
        />
        <Header meta={meta} simNow={simNow} speed={stats?.speed ?? meta.clock.speed} go={go} onAbout={() => setAbout(true)}>
          <NavLink href="/" go={go}>
            Phones
          </NavLink>
        </Header>
        <div className="absolute bottom-4 left-4 z-10 rounded-[14px] bg-panel/90 px-3 pt-2.5 pb-2 backdrop-blur">
          <Legend />
        </div>
      </div>
      {wide && (
        <aside className="phone-scroll w-[400px] shrink-0 overflow-y-auto border-l border-line bg-panel p-5" aria-label="Operations">
          <h1 className="text-[22px] font-extrabold leading-tight">Operations</h1>
          <p className="mt-1 text-[13px] text-mute">
            Replaying {meta.tripsDay.toLocaleString("en-US")} real Uber trips on {meta.graph.nodes.toLocaleString("en-US")} intersections and{" "}
            {meta.graph.edges.toLocaleString("en-US")} road segments.
          </p>
          {stats ? (
            <>
              <div className="mt-5 [&_.grid]:sm:grid-cols-2">
                <Readouts stats={stats} meta={meta} simNow={simNow} />
              </div>
              <div className="mt-5 flex flex-col gap-4 [&>div]:flex-col">
                <Charts stats={stats} meta={meta} simNow={simNow} />
              </div>
              <h2 className="mt-6 text-[15px] font-bold">Controls</h2>
              <div className="mt-2">
                <Controls stats={stats} meta={meta} />
              </div>
              <h2 className="mt-6 text-[15px] font-bold">Since the server started</h2>
              <div className="mt-2">
                <Totals stats={stats} />
              </div>
            </>
          ) : (
            <p className="mt-4 text-[14px] text-mute">Connecting to the city...</p>
          )}
          <h2 className="mt-6 text-[15px] font-bold">Happening now</h2>
          <div className="mt-2">
            <Feed items={ops.feed} limit={14} />
          </div>
        </aside>
      )}
      {about && <About meta={meta} onClose={() => setAbout(false)} />}
    </main>
  );
}

function Solo({ meta, go, kind }: { meta: Meta; go: Go; kind: "rider" | "driver" }) {
  const framed = useWide(640);
  const app = kind === "rider" ? <RiderApp meta={meta} /> : <DriverApp meta={meta} />;
  if (!framed) return <Phone label={kind === "rider" ? "Rider app" : "Driver app"} framed={false}>{app}</Phone>;
  return (
    <main className="flex h-full flex-col items-center justify-center gap-4 bg-night p-5">
      <a href="/" onClick={go("/")} aria-label="Crosstown home">
        <Plate />
      </a>
      <div className="h-[min(820px,calc(100%-64px))] w-[380px]">
        <Phone label={kind === "rider" ? "Rider app" : "Driver app"} framed>
          {app}
        </Phone>
      </div>
    </main>
  );
}

function MobileShell({ meta, go }: { meta: Meta; go: Go }) {
  const [tab, setTab] = useState<"ride" | "drive" | "city">("ride");
  const tabs: [typeof tab, string][] = [
    ["ride", "Ride"],
    ["drive", "Drive"],
    ["city", "City"],
  ];
  return (
    <main className="flex h-full flex-col bg-night">
      <div className="relative min-h-0 flex-1">
        {tab === "ride" && (
          <Phone label="Rider app" framed={false}>
            <RiderApp meta={meta} />
          </Phone>
        )}
        {tab === "drive" && (
          <Phone label="Driver app" framed={false}>
            <DriverApp meta={meta} />
          </Phone>
        )}
        {tab === "city" && <MobileCity meta={meta} go={go} />}
      </div>
      <nav className="grid grid-cols-3 border-t border-line bg-panel pb-[env(safe-area-inset-bottom)]" aria-label="Views">
        {tabs.map(([id, label]) => (
          <button
            key={id}
            type="button"
            aria-pressed={tab === id}
            onClick={() => setTab(id)}
            className={`pt-[14px] pb-[12px] text-[15px] font-bold ${tab === id ? "text-ink" : "text-mute"}`}
          >
            {label}
          </button>
        ))}
      </nav>
    </main>
  );
}

function MobileCity({ meta, go }: { meta: Meta; go: Go }) {
  const ops = useOpsFeed();
  const { live, surge } = useLive("ops", undefined, "dark", ops.onMessage);
  useNow(1000);
  const simNow = live.clock.now() || meta.clock.now;
  return (
    <div className="relative h-full">
      <OpsMap
        meta={meta}
        live={live}
        surge={surge}
        pings={ops.pings}
        className="absolute inset-0"
        view={{ center: [-73.977, 40.757], zoom: 11.9, padding: { top: 60, bottom: 240, left: 0, right: 0 } }}
      />
      <div className="absolute inset-x-0 top-0 z-10 flex items-center gap-3 bg-gradient-to-b from-night to-night/0 px-4 pt-3 pb-8">
        <a href="/" onClick={go("/")} aria-label="Crosstown home">
          <Plate small />
        </a>
        <div className="text-[13px] leading-tight">
          <div className="font-semibold">Manhattan, {clock(simNow)}</div>
          <div className="text-mute">Replay of {day(meta.replayDay)}</div>
        </div>
      </div>
      {ops.stats && (
        <div className="absolute inset-x-3 bottom-3 z-10 rounded-[18px] bg-panel/92 p-3 backdrop-blur">
          <Readouts stats={ops.stats} meta={meta} simNow={simNow} />
          <div className="mt-3">
            <Legend />
          </div>
        </div>
      )}
    </div>
  );
}
