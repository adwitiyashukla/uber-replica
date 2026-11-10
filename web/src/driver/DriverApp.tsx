import { useEffect, useMemo, useRef, useState } from "react";
import type { Layer } from "@deck.gl/core";
import type maplibregl from "maplibre-gl";
import { api, stableId } from "../lib/api";
import { clock, distance, minutes, money, productName } from "../lib/format";
import { ahead, meters, project } from "../lib/path";
import type { DriverView, Meta, Offer, Product, RouteView, Trip } from "../lib/types";
import { useLive, useNow } from "../hooks";
import { CityMap } from "../map/CityMap";
import { carIconLayer, carsLayer, pinsLayer, routeLayer, surgeLayer } from "../map/layers";
import { Button, Toast } from "../ui/kit";

const products: Product[] = ["standard", "comfort", "xl"];

function nearestIndex(points: [number, number][], at: [number, number]) {
  const k = Math.cos((at[1] * Math.PI) / 180);
  let best = 0;
  let bd = Infinity;
  for (let i = 0; i < points.length; i++) {
    const dx = (points[i][0] - at[0]) * k;
    const dy = points[i][1] - at[1];
    const d = dx * dx + dy * dy;
    if (d < bd) {
      bd = d;
      best = i;
    }
  }
  return best;
}

function useNav(route: RouteView | null) {
  return useMemo(() => {
    if (!route || route.points.length < 2) return null;
    const starts = route.steps.map((s) => nearestIndex(route.points, [s.lng, s.lat]));
    const cum = [0];
    for (let i = 1; i < route.points.length; i++) cum.push(cum[i - 1] + meters(route.points[i - 1], route.points[i]));
    return { starts, cum };
  }, [route]);
}

export function DriverApp({ meta }: { meta: Meta }) {
  const driverId = useMemo(() => stableId("driver"), []);
  const [view, setView] = useState<DriverView | null>(null);
  const [offer, setOffer] = useState<Offer | null>(null);
  const [route, setRoute] = useState<RouteView | null>(null);
  const [product, setProduct] = useState<Product>("standard");
  const [toast, setToast] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [follow, setFollow] = useState(true);
  const mapRef = useRef<maplibregl.Map | null>(null);
  const state = useRef({ view, follow, route, offer });
  state.current = { view, follow, route, offer };

  const { live, surge } = useLive("driver", driverId, "light", (m) => {
    if (m.t === "driver") {
      const v = m.driver as DriverView;
      setView(v);
      setOffer(v.offer ?? null);
      if (v.state === "idle" || v.state === "offline") setRoute(null);
    }
    if (m.t === "offer") setOffer(m.offer as Offer);
    if (m.t === "offer_closed") {
      setOffer((o) => (o && o.id === m.id ? null : o));
      if (m.reason === "expired") setToast("That request expired, it went to another driver.");
    }
    if (m.t === "route") setRoute(m.route as RouteView);
    if (m.t === "trip") {
      const t = m.trip as Trip;
      if (t.driver?.id !== driverId) return;
      if (m.event === "trip_completed") setToast(`Trip complete. You earned ${money(t.driverPay)}.`);
      if (m.event === "trip_cancelled") setToast("The rider cancelled. You are free for the next request.");
    }
  });
  const now = useNow(offer ? 250 : 1000);
  const nav = useNav(route);

  useEffect(() => {
    api
      .driver(driverId)
      .then((v) => {
        setView(v ?? null);
        setOffer(v?.offer ?? null);
      })
      .catch(() => setView(null));
  }, [driverId]);

  const act = async (f: () => Promise<DriverView>) => {
    setBusy(true);
    try {
      const v = await f();
      setView(v);
      setOffer(v.offer ?? null);
    } catch (err) {
      setToast((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const online = view && view.state !== "offline";
  const myPos = () => (view && online ? live.fleet.locate(view.num, performance.now()) ?? [view.lng, view.lat] : null);

  const layers = (t: number) => {
    const s = state.current;
    const out: Layer[] = [surgeLayer("driver-surge", surge, 0.6), carsLayer("driver-cars", live.fleet, t, 14, 6, 0.75)];
    const v = s.view;
    if (!v || v.state === "offline") return out;
    const at = live.fleet.locate(v.num, t) ?? ([v.lng, v.lat] as [number, number]);
    if (s.route && (v.state === "en_route" || v.state === "on_trip")) {
      out.push(routeLayer("driver-route", ahead(s.route.points, at), s.route.purpose === "pickup" ? [10, 107, 69] : [47, 116, 207], 6));
    }
    const trip = v.trip;
    if (trip) {
      const pickupLeg = v.state === "en_route" || v.state === "at_pickup";
      const p = pickupLeg ? trip.pickup.point : trip.dropoff.point;
      out.push(pinsLayer("driver-pins", [{ kind: pickupLeg ? "pickup" : "dropoff", at: [p.lng, p.lat] }]));
    }
    if (s.offer) out.push(pinsLayer("driver-offer", [{ kind: "pickup", at: [s.offer.pickup.point.lng, s.offer.pickup.point.lat] }]));
    out.push(carIconLayer("driver-me", at, live.fleet.headingOf(v.num), true));
    const map = mapRef.current;
    if (map && s.follow && !map.isMoving()) map.jumpTo({ center: at });
    return out;
  };

  const simNow = live.clock.now() || meta.clock.now;
  const pos = myPos();
  let banner: { text: string; meters: number } | null = null;
  if (route && nav && pos && view && (view.state === "en_route" || view.state === "on_trip")) {
    const { seg } = project(route.points, pos);
    const head = Math.min(seg + 1, route.points.length - 1);
    const along = (j: number) => Math.max(0, meters(pos, route.points[head]) + nav.cum[j] - nav.cum[head]);
    const next = nav.starts.findIndex((s) => s > seg);
    if (next >= 0) banner = { text: route.steps[next].text, meters: along(nav.starts[next]) };
    else banner = { text: route.purpose === "pickup" ? "Arrive at the pickup" : "Arrive at the drop-off", meters: along(nav.cum.length - 1) };
  }
  void now;

  return (
    <div className="relative flex h-full flex-col">
      <CityMap
        label="Driver map"
        styleUrl={meta.mapStyleLight}
        theme="light"
        center={[-73.9855, 40.7565]}
        zoom={15.2}
        minZoom={11}
        padding={{ top: 90, bottom: 300, left: 0, right: 0 }}
        layers={layers}
        onMap={(m) => {
          mapRef.current = m;
          m.on("dragstart", () => setFollow(false));
        }}
        onView={(b) => live.channel?.setView(b)}
        className="absolute inset-0"
      />
      <Toast text={toast} onClose={() => setToast(null)} />
      {banner && !toast && (
        <div className="absolute inset-x-3 top-3 z-10 rounded-[16px] bg-sign px-4 pt-[12px] pb-[10px] text-white shadow-lg">
          <div className="text-[13px] opacity-85">{banner.meters < 15 ? "Now" : `In ${distance(banner.meters)}`}</div>
          <div className="text-[18px] font-bold leading-snug">{banner.text}</div>
        </div>
      )}
      {online && !follow && (
        <button
          type="button"
          onClick={() => setFollow(true)}
          className="absolute right-3 bottom-[46%] z-10 rounded-full bg-paper px-4 pt-[9px] pb-[7px] text-[14px] font-semibold shadow-md"
        >
          Re-center
        </button>
      )}
      <div className="phone-scroll absolute inset-x-0 bottom-0 z-10 max-h-[70%] overflow-y-auto rounded-t-[26px] bg-paper px-4 pt-3 pb-5 shadow-[0_-10px_30px_rgba(16,32,43,0.18)]">
        <div className="mx-auto mb-3 h-1 w-10 rounded-full bg-[#d5dde1]" />
        {offer ? (
          <OfferCard offer={offer} simNow={simNow} busy={busy} onAccept={() => act(() => api.accept(driverId, offer.id))} onDecline={() => act(() => api.decline(driverId, offer.id))} />
        ) : !online ? (
          <div>
            <h2 className="text-[24px] font-extrabold leading-tight">You are offline</h2>
            <p className="mt-1 text-[14px] text-slate">
              Go online to take real trip requests from the replay. You start near a busy part of Manhattan and the car drives itself, so you only make the calls.
            </p>
            <div className="mt-3 grid grid-cols-3 gap-2" role="radiogroup" aria-label="Car type">
              {products.map((p) => (
                <button
                  key={p}
                  type="button"
                  role="radio"
                  aria-checked={product === p}
                  onClick={() => setProduct(p)}
                  className={`rounded-[12px] border-2 px-2 pt-[9px] pb-[7px] text-[14px] font-bold ${product === p ? "border-ash" : "border-[#e1e7ea] text-slate"}`}
                >
                  {productName[p]}
                </button>
              ))}
            </div>
            {view && view.trips > 0 && (
              <p className="mt-3 text-[14px] text-slate">
                Last session: {view.trips} {view.trips === 1 ? "trip" : "trips"}, {money(view.earnings)} earned.
              </p>
            )}
            <div className="mt-3">
              <Button onClick={() => act(() => api.online(driverId, product))} disabled={busy}>
                {busy ? "Going online..." : "Go online"}
              </Button>
            </div>
          </div>
        ) : (
          <OnlinePanel view={view!} simNow={simNow} busy={busy} driverId={driverId} act={act} />
        )}
      </div>
    </div>
  );
}

function Ring({ left }: { left: number }) {
  const r = 26;
  const c = 2 * Math.PI * r;
  return (
    <svg width="64" height="64" viewBox="0 0 64 64" aria-hidden="true">
      <circle cx="32" cy="32" r={r} fill="none" stroke="#e3e9eb" strokeWidth="6" />
      <circle
        cx="32"
        cy="32"
        r={r}
        fill="none"
        stroke="#0a6b45"
        strokeWidth="6"
        strokeLinecap="round"
        strokeDasharray={c}
        strokeDashoffset={c * (1 - left)}
        transform="rotate(-90 32 32)"
      />
    </svg>
  );
}

function OfferCard({ offer, simNow, busy, onAccept, onDecline }: { offer: Offer; simNow: number; busy: boolean; onAccept: () => void; onDecline: () => void }) {
  const total = Math.max(1, offer.expiresAt - offer.sentAt);
  const left = Math.max(0, Math.min(1, (offer.expiresAt - simNow) / total));
  const secs = Math.max(0, Math.ceil((offer.expiresAt - simNow) / 1000));
  return (
    <div className="rise">
      <div className="flex items-center gap-3">
        <div className="relative shrink-0">
          <Ring left={left} />
          <span className="absolute inset-0 flex items-center justify-center pt-0.5 text-[16px] font-bold" aria-label={`${secs} seconds left`}>
            {secs}
          </span>
        </div>
        <div className="min-w-0">
          <div className="text-[28px] leading-none font-extrabold">{money(offer.earnings)}</div>
          <div className="mt-1 text-[13px] text-slate">
            {productName[offer.product]} trip{offer.surge > 1 ? `, ${offer.surge.toFixed(1)}x busy area` : ""}
          </div>
        </div>
      </div>
      <dl className="mt-3 space-y-2 text-[14px]">
        <div className="flex gap-3">
          <dt className="w-16 shrink-0 text-slate">Pickup</dt>
          <dd className="min-w-0">
            <div className="font-semibold">{offer.pickup.name}</div>
            <div className="text-slate">{offer.b2b ? `After your drop-off, about ${minutes(offer.eta)} total` : `${minutes(offer.eta)} away`}</div>
          </dd>
        </div>
        <div className="flex gap-3">
          <dt className="w-16 shrink-0 text-slate">Drop-off</dt>
          <dd className="min-w-0">
            <div className="font-semibold">{offer.dropoff.name}</div>
            <div className="text-slate">
              {minutes(offer.tripSeconds)}, {distance(offer.tripMeters)}
            </div>
          </dd>
        </div>
      </dl>
      <div className="mt-4 grid grid-cols-[1fr_2fr] gap-2">
        <Button kind="quiet" onClick={onDecline} disabled={busy}>
          Decline
        </Button>
        <Button onClick={onAccept} disabled={busy}>
          Accept
        </Button>
      </div>
    </div>
  );
}

function OnlinePanel({
  view,
  simNow,
  busy,
  driverId,
  act,
}: {
  view: DriverView;
  simNow: number;
  busy: boolean;
  driverId: string;
  act: (f: () => Promise<DriverView>) => void;
}) {
  const trip = view.trip;
  const stats = (
    <div className="mt-3 grid grid-cols-3 gap-2 text-center">
      {[
        ["Earned", money(view.earnings)],
        ["Trips", String(view.trips)],
        ["Online", minutes(view.onlineMs / 1000)],
      ].map(([k, v]) => (
        <div key={k} className="rounded-[12px] bg-fog px-2 pt-[9px] pb-[7px]">
          <div className="text-[17px] font-bold">{v}</div>
          <div className="text-[12px] text-slate">{k}</div>
        </div>
      ))}
    </div>
  );
  const offline = (
    <div className="mt-3">
      <Button kind="quiet" onClick={() => act(() => api.offline(driverId))} disabled={busy}>
        {view.goOffline ? "Going offline after this trip" : "Go offline"}
      </Button>
    </div>
  );
  if (!trip) {
    return (
      <div>
        <h2 className="text-[22px] font-extrabold">You are online</h2>
        <p className="mt-1 text-[14px] text-slate">Waiting for a request. Red areas on the map are busy right now.</p>
        {stats}
        {offline}
      </div>
    );
  }
  if (view.state === "en_route") {
    return (
      <div>
        <h2 className="text-[22px] font-extrabold">Picking up in {minutes(Math.max(30, trip.eta))}</h2>
        <p className="mt-1 text-[14px] text-slate">Meet your rider at {trip.pickup.name}. The car is driving there now.</p>
        {view.next && <p className="mt-2 text-[14px] text-slate">Next trip queued from {view.next.pickup.name}.</p>}
        {stats}
      </div>
    );
  }
  if (view.state === "at_pickup") {
    return (
      <div>
        <h2 className="text-[22px] font-extrabold">Your rider is getting in</h2>
        <p className="mt-1 text-[14px] text-slate">Start the trip once they are in the car. It starts by itself after 90 seconds if you step away.</p>
        <div className="mt-3">
          <Button onClick={() => act(() => api.start(driverId, trip.id))} disabled={busy}>
            Start trip
          </Button>
        </div>
      </div>
    );
  }
  const near = trip.eta >= 0 && trip.eta < 25;
  return (
    <div>
      <h2 className="text-[22px] font-extrabold">Dropping off at {trip.dropoff.name}</h2>
      <p className="mt-1 text-[14px] text-slate">
        Arriving around {clock(simNow + Math.max(0, trip.eta) * 1000)}. You earn {money(trip.driverPay)} on this trip.
      </p>
      {view.next && <p className="mt-2 text-[14px] text-slate">Next pickup queued at {view.next.pickup.name}.</p>}
      <div className="mt-3">
        <Button onClick={() => act(() => api.complete(driverId, trip.id))} disabled={busy || !near}>
          {near ? "Complete trip" : "Complete trip at the drop-off"}
        </Button>
      </div>
      {offline}
    </div>
  );
}
