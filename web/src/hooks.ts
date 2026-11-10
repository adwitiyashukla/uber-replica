import { useEffect, useRef, useState } from "react";
import { api } from "./lib/api";
import { Channel, Fleet, SimClock, type Msg } from "./lib/live";
import type { Meta, SurgeCell } from "./lib/types";

export function useMeta() {
  const [meta, setMeta] = useState<Meta | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .meta()
        .then((m) => alive && setMeta(m))
        .catch(() => {
          if (!alive) return;
          setError("The server is not answering yet. Retrying.");
          window.setTimeout(load, 2000);
        });
    load();
    return () => {
      alive = false;
    };
  }, []);
  return { meta, error };
}

export interface Live {
  channel: Channel;
  fleet: Fleet;
  clock: SimClock;
}

export function useLive(role: "ops" | "rider" | "driver", id: string | undefined, theme: "dark" | "light", onMessage: (m: Msg) => void) {
  const handler = useRef(onMessage);
  handler.current = onMessage;
  const [surge, setSurge] = useState<SurgeCell[]>([]);
  const [connected, setConnected] = useState(false);
  const live = useRef<Live | null>(null);
  if (!live.current) {
    live.current = { channel: null as unknown as Channel, fleet: new Fleet(theme, role === "ops"), clock: new SimClock() };
  }
  useEffect(() => {
    const ch = new Channel(role, id);
    live.current!.channel = ch;
    const offMsg = ch.on((m) => {
      if (m.t === "hello" || m.t === "clock") live.current!.clock.set(m.now as number, m.speed as number);
      if (m.t === "surge") setSurge((m.cells as SurgeCell[]) ?? []);
      if (m.t === "open") setConnected(true);
      if (m.t === "close") setConnected(false);
      handler.current(m);
    });
    const offFrame = ch.onFrame((f) => live.current!.fleet.update(f));
    return () => {
      offMsg();
      offFrame();
      ch.close();
    };
  }, [role, id]);
  return { live: live.current, surge, connected };
}

export function useNow(ms = 1000) {
  const [now, setNow] = useState(() => performance.now());
  useEffect(() => {
    const t = window.setInterval(() => setNow(performance.now()), ms);
    return () => window.clearInterval(t);
  }, [ms]);
  return now;
}

export function useWide(min = 1180) {
  const query = `(min-width: ${min}px)`;
  const [wide, setWide] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const mq = window.matchMedia(query);
    const on = () => setWide(mq.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, [query]);
  return wide;
}
