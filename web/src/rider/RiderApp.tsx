import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Layer } from "@deck.gl/core";
import type maplibregl from "maplibre-gl";
import { api, ApiError, requestRef, stableId } from "../lib/api";
import { clock, distance, minutes, money, productName } from "../lib/format";
import { ahead } from "../lib/path";
import type { Meta, PlaceHit, Point, Product, Quote, RouteView, Trip } from "../lib/types";
import { useLive, useNow } from "../hooks";
import { CityMap } from "../map/CityMap";
import { carIconLayer, carsLayer, pinsLayer, pulseLayer, routeLayer, surgeLayer } from "../map/layers";
import { Button, Stars, Toast } from "../ui/kit";

interface Spot {
  name: string;
  point: Point;
}

const landmarks: Spot[] = [
  { name: "Times Square", point: { lat: 40.758, lng: -73.9855 } },
  { name: "Grand Central Terminal", point: { lat: 40.7527, lng: -73.9772 } },
  { name: "New York Penn Station", point: { lat: 40.7506, lng: -73.9935 } },
  { name: "Union Square", point: { lat: 40.7359, lng: -73.9911 } },
  { name: "Columbus Circle", point: { lat: 40.7681, lng: -73.9819 } },
  { name: "Washington Square Park", point: { lat: 40.7308, lng: -73.9973 } },
  { name: "Rockefeller Center", point: { lat: 40.7587, lng: -73.9787 } },
  { name: "The Met", point: { lat: 40.7794, lng: -73.9632 } },
  { name: "Chelsea Market", point: { lat: 40.7424, lng: -74.0061 } },
  { name: "Columbia University", point: { lat: 40.8075, lng: -73.9626 } },
  { name: "One World Trade Center", point: { lat: 40.7127, lng: -74.0134 } },
  { name: "Lincoln Center", point: { lat: 40.7725, lng: -73.9835 } },
  { name: "Apollo Theater", point: { lat: 40.81, lng: -73.95 } },
  { name: "Whitney Museum", point: { lat: 40.7396, lng: -74.0089 } },
];

const kindLabel: Record<string, string> = {
  station: "Station",
  attraction: "Landmark",
  museum: "Museum",
  park: "Park",
  hotel: "Hotel",
  theatre: "Theater",
  restaurant: "Restaurant",
  cafe: "Cafe",
  bar: "Bar",
  corner: "Intersection",
  area: "Neighborhood",
  university: "University",
  hospital: "Hospital",
};

const seats: Record<Product, number> = { standard: 4, comfort: 4, xl: 6 };

function lngLat(p: Point): [number, number] {
  return [p.lng, p.lat];
}

export function RiderApp({ meta }: { meta: Meta }) {
  const riderId = useMemo(() => stableId("rider"), []);
  const [pickup, setPickup] = useState<Spot>(() => landmarks[Math.floor(Math.random() * 7)]);
  const [dropoff, setDropoff] = useState<Spot | null>(null);
  const [editing, setEditing] = useState<"pickup" | "dropoff" | null>(null);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<PlaceHit[]>([]);
  const [quote, setQuote] = useState<Quote | null>(null);
  const [quoteRoute, setQuoteRoute] = useState<[number, number][] | null>(null);
  const [product, setProduct] = useState<Product>("standard");
  const [trip, setTrip] = useState<Trip | null>(null);
  const [route, setRoute] = useState<RouteView | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const mapRef = useRef<maplibregl.Map | null>(null);
  const sheetRef = useRef<HTMLDivElement | null>(null);
  const tripRef = useRef<Trip | null>(null);
  tripRef.current = trip;

  const { live, surge } = useLive("rider", riderId, "light", (m) => {
    if (m.t === "trip") {
      const t = m.trip as Trip;
      if (t.riderId !== riderId) return;
      setTrip(t);
      if (t.state === "completed" || t.state === "cancelled") setRoute(null);
    }
    if (m.t === "route") {
      const r = m.route as RouteView;
      if (!tripRef.current || r.tripId === tripRef.current.id) setRoute(r);
    }
  });
  const now = useNow(1000);

  useEffect(() => {
    api.active(riderId).then((t) => t && setTrip(t)).catch(() => undefined);
  }, [riderId]);

  useEffect(() => {
    if (!editing || query.trim().length < 2) {
      setHits([]);
      return;
    }
    const t = window.setTimeout(() => {
      api
        .places(query, pickup.point)
        .then((r) => setHits(r.places))
        .catch(() => setHits([]));
    }, 140);
    return () => window.clearTimeout(t);
  }, [query, editing, pickup.point]);

  const fit = useCallback((points: [number, number][]) => {
    const map = mapRef.current;
    if (!map || points.length === 0) return;
    let w = 180, s = 90, e = -180, n = -90;
    for (const [x, y] of points) {
      w = Math.min(w, x);
      e = Math.max(e, x);
      s = Math.min(s, y);
      n = Math.max(n, y);
    }
    const one = w === e && s === n;
    const top = one ? 40 : 70;
    const sheet = sheetRef.current?.offsetHeight ?? 300;
    const bottom = Math.max(0, Math.min(sheet + 24, map.getContainer().clientHeight - top - 60));
    map.fitBounds(
      [
        [w, s],
        [e, n],
      ],
      { padding: { top, bottom, left: one ? 20 : 40, right: one ? 20 : 40 }, maxZoom: one ? 15 : 16, duration: 700 },
    );
  }, []);

  useEffect(() => {
    if (trip || !dropoff) {
      setQuote(null);
      setQuoteRoute(null);
      return;
    }
    let alive = true;
    setBusy(true);
    setNotice(null);
    api
      .quote(riderId, pickup.point, dropoff.point, pickup.name, dropoff.name === "Dropped pin" ? "" : dropoff.name)
      .then((r) => {
        if (!alive) return;
        setQuote(r.quote);
        setQuoteRoute(r.route);
      })
      .catch((err: Error) => alive && setNotice(err.message))
      .finally(() => alive && setBusy(false));
    return () => {
      alive = false;
    };
  }, [pickup, dropoff, trip, riderId]);

  useEffect(() => {
    if (quoteRoute) fit(quoteRoute);
  }, [quoteRoute, fit]);

  const routeKey = route && trip && route.tripId === trip.id ? route.purpose : "";
  useEffect(() => {
    if (!trip) {
      fit([lngLat(pickup.point)]);
      return;
    }
    const along = routeKey && route ? route.points : [];
    if (trip.state === "on_trip") fit([lngLat(trip.pickup.point), lngLat(trip.dropoff.point), ...(routeKey === "dropoff" ? along : [])]);
    else if (trip.state === "accepted" && trip.driver) fit([lngLat(trip.pickup.point), [trip.driver.lng, trip.driver.lat], ...(routeKey === "pickup" ? along : [])]);
    else if (trip.state === "searching" || trip.state === "offering" || trip.state === "arrived") fit([lngLat(trip.pickup.point)]);
    else if (trip.state === "completed") fit([lngLat(trip.dropoff.point)]);
  }, [trip?.id, trip?.state, routeKey]);

  const choose = (spot: Spot) => {
    if (editing === "pickup") setPickup(spot);
    else setDropoff(spot);
    setEditing(null);
    setQuery("");
    setHits([]);
  };

  const requestRide = async () => {
    const option = quote?.options.find((o) => o.product === product);
    if (!option) return;
    setBusy(true);
    setNotice(null);
    try {
      const t = await api.request(riderId, option.token, requestRef());
      setTrip(t);
    } catch (err) {
      const e = err as ApiError;
      setNotice(e.status === 409 && /expired/.test(e.message) ? "That price expired. Here is a fresh one." : e.message);
      if (e.status === 409 && /active trip/.test(e.message)) api.active(riderId).then((t) => t && setTrip(t));
      if (dropoff) setDropoff({ ...dropoff });
    } finally {
      setBusy(false);
    }
  };

  const cancel = async () => {
    if (!trip) return;
    try {
      setTrip(await api.cancel(riderId, trip.id));
    } catch (err) {
      setNotice((err as Error).message);
    }
  };

  const reset = () => {
    setTrip(null);
    setRoute(null);
    setDropoff(null);
    setQuote(null);
    setNotice(null);
  };

  const rate = async (stars: number) => {
    if (!trip) return;
    try {
      await api.rate(riderId, trip.id, stars);
      setTrip({ ...trip, rating: stars });
    } catch (err) {
      setNotice((err as Error).message);
    }
  };

  const layers = (t: number) => {
    const out: Layer[] = [
      surgeLayer("rider-surge", surge, 0.4),
      carsLayer("rider-cars", live.fleet, t, 14, 6, 0.85),
      routeLayer("rider-quote", trip ? null : quoteRoute, [16, 32, 43], 5),
    ];
    const tr = tripRef.current;
    if (tr) {
      const moving = tr.driver && (tr.state === "accepted" || tr.state === "arrived" || tr.state === "on_trip");
      const car = moving && tr.driver ? (live.fleet.locate(tr.driver.num, t) ?? ([tr.driver.lng, tr.driver.lat] as [number, number])) : null;
      const color: [number, number, number] = route?.purpose === "dropoff" ? [16, 32, 43] : [10, 107, 69];
      const path = route && (tr.state === "accepted" || tr.state === "on_trip") ? (car ? ahead(route.points, car) : route.points) : null;
      out.push(routeLayer("rider-route", path, color, 5));
      const pins: { kind: "pickup" | "dropoff"; at: [number, number] }[] = [];
      if (tr.state !== "on_trip" && tr.state !== "completed") pins.push({ kind: "pickup", at: lngLat(tr.pickup.point) });
      pins.push({ kind: "dropoff", at: lngLat(tr.dropoff.point) });
      out.push(pinsLayer("rider-pins", pins));
      if (tr.state === "searching" || tr.state === "offering") out.push(pulseLayer("rider-pulse", lngLat(tr.pickup.point), t, [10, 107, 69]));
      if (car && tr.driver) out.push(carIconLayer("rider-car", car, live.fleet.headingOf(tr.driver.num), false));
    } else {
      const pins: { kind: "pickup" | "dropoff"; at: [number, number] }[] = [{ kind: "pickup", at: lngLat(quote?.pickup.point ?? pickup.point) }];
      if (dropoff) pins.push({ kind: "dropoff", at: lngLat(quote?.dropoff.point ?? dropoff.point) });
      out.push(pinsLayer("rider-pins", pins));
    }
    return out;
  };

  const simNow = live.clock.now() || meta.clock.now;
  void now;

  return (
    <div className="relative flex h-full flex-col">
      <CityMap
        label="Rider map"
        styleUrl={meta.mapStyleLight}
        theme="light"
        center={lngLat(pickup.point)}
        zoom={14.6}
        minZoom={11}
        layers={layers}
        onMap={(m) => (mapRef.current = m)}
        onView={(b) => live.channel?.setView(b)}
        onClick={(lng, lat) => {
          if (!trip && !editing) setDropoff({ name: "Dropped pin", point: { lat, lng } });
        }}
        className="absolute inset-0"
      />
      <Toast text={notice} onClose={() => setNotice(null)} />
      <div
        ref={sheetRef}
        className="phone-scroll absolute inset-x-0 bottom-0 z-10 max-h-[78%] overflow-y-auto rounded-t-[26px] bg-paper px-4 pt-3 pb-5 shadow-[0_-10px_30px_rgba(16,32,43,0.18)]"
      >
        <div className="mx-auto mb-3 h-1 w-10 rounded-full bg-[#d5dde1]" />
        {editing ? (
          <SearchPanel
            editing={editing}
            query={query}
            setQuery={setQuery}
            hits={hits}
            choose={choose}
            close={() => {
              setEditing(null);
              setQuery("");
            }}
          />
        ) : trip ? (
          <TripPanel trip={trip} simNow={simNow} onCancel={cancel} onDone={reset} onRate={rate} />
        ) : quote && dropoff ? (
          <OptionsPanel
            quote={quote}
            product={product}
            setProduct={setProduct}
            busy={busy}
            onRequest={requestRide}
            onEdit={() => setEditing("dropoff")}
            simNow={simNow}
            pickupName={quote.pickup.name}
            dropoffName={quote.dropoff.name}
          />
        ) : (
          <HomePanel pickup={pickup} busy={busy && !!dropoff} onWhere={() => setEditing("dropoff")} onPickup={() => setEditing("pickup")} choose={(s) => setDropoff(s)} exclude={pickup.name} />
        )}
      </div>
    </div>
  );
}

function HomePanel({
  pickup,
  busy,
  onWhere,
  onPickup,
  choose,
  exclude,
}: {
  pickup: Spot;
  busy: boolean;
  onWhere: () => void;
  onPickup: () => void;
  choose: (s: Spot) => void;
  exclude: string;
}) {
  const ideas = landmarks.filter((l) => l.name !== exclude).slice(6, 12);
  return (
    <div>
      <h2 className="text-[24px] font-extrabold leading-tight tracking-[-0.01em]">Where to?</h2>
      <button type="button" onClick={onWhere} className="mt-3 flex w-full items-center gap-3 rounded-[14px] bg-fog px-4 pt-[14px] pb-[12px] text-left text-[16px] text-slate">
        <span className="inline-block h-2.5 w-2.5 shrink-0 rounded-[2px] bg-ash" />
        {busy ? "Getting prices..." : "Search a place, street corner or neighborhood"}
      </button>
      <button type="button" onClick={onPickup} className="mt-2 flex w-full items-center gap-3 rounded-[14px] px-4 pt-[10px] pb-[8px] text-left text-[14px]">
        <span className="inline-block h-2.5 w-2.5 shrink-0 rounded-full bg-sign" />
        <span className="text-slate">Pickup</span>
        <span className="font-semibold">{pickup.name}</span>
        <span className="ml-auto text-sign">Change</span>
      </button>
      <p className="mt-3 text-[13px] text-slate">Popular right now, or tap anywhere on the map.</p>
      <div className="mt-2 flex flex-wrap gap-2">
        {ideas.map((l) => (
          <button key={l.name} type="button" onClick={() => choose(l)} className="rounded-full border border-[#dbe2e5] px-3 pt-[7px] pb-[5px] text-[14px] hover:bg-fog">
            {l.name}
          </button>
        ))}
      </div>
    </div>
  );
}

function SearchPanel({
  editing,
  query,
  setQuery,
  hits,
  choose,
  close,
}: {
  editing: "pickup" | "dropoff";
  query: string;
  setQuery: (q: string) => void;
  hits: PlaceHit[];
  choose: (s: Spot) => void;
  close: () => void;
}) {
  return (
    <div>
      <div className="flex items-center gap-2">
        <label className="sr-only" htmlFor="place-search">
          {editing === "pickup" ? "Pickup" : "Destination"}
        </label>
        <input
          id="place-search"
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={editing === "pickup" ? "Where should we pick you up?" : "Where are you going?"}
          className="min-w-0 flex-1 rounded-[14px] bg-fog px-4 pt-[13px] pb-[11px] text-[16px] outline-none focus:ring-2 focus:ring-sign"
        />
        <button type="button" onClick={close} className="rounded-[12px] px-3 py-2 text-[15px] font-semibold text-slate hover:bg-fog">
          Close
        </button>
      </div>
      <ul className="mt-2">
        {hits.map((h) => (
          <li key={`${h.name}-${h.lat}`}>
            <button
              type="button"
              onClick={() => choose({ name: h.name, point: { lat: h.lat, lng: h.lng } })}
              className="flex w-full items-baseline justify-between gap-3 rounded-[12px] px-2 pt-[11px] pb-[9px] text-left hover:bg-fog"
            >
              <span className="text-[15px] font-semibold">{h.name}</span>
              <span className="shrink-0 text-[12px] text-slate">{kindLabel[h.kind] ?? "Place"}</span>
            </button>
          </li>
        ))}
        {query.trim().length >= 2 && hits.length === 0 && (
          <li className="px-2 py-3 text-[14px] text-slate">No Manhattan matches yet. Try a street corner like 42nd and 8th.</li>
        )}
      </ul>
    </div>
  );
}

function OptionsPanel({
  quote,
  product,
  setProduct,
  busy,
  onRequest,
  onEdit,
  simNow,
  pickupName,
  dropoffName,
}: {
  quote: Quote;
  product: Product;
  setProduct: (p: Product) => void;
  busy: boolean;
  onRequest: () => void;
  onEdit: () => void;
  simNow: number;
  pickupName: string;
  dropoffName: string;
}) {
  return (
    <div>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="truncate text-[13px] text-slate">From {pickupName}</p>
          <h2 className="truncate text-[20px] font-extrabold leading-tight">{dropoffName}</h2>
          <p className="mt-0.5 text-[13px] text-slate">
            {distance(quote.meters)}, about {minutes(quote.seconds)} in current traffic
          </p>
        </div>
        <button type="button" onClick={onEdit} className="shrink-0 rounded-[12px] px-3 py-2 text-[14px] font-semibold text-sign hover:bg-fog">
          Change
        </button>
      </div>
      {quote.surge > 1 && (
        <p className="mt-3 rounded-[12px] bg-[#fdecef] px-3 pt-[9px] pb-[7px] text-[13px] text-[#8c1d33]">
          It is busy near you, so prices are {quote.surge.toFixed(1)}x for now.
        </p>
      )}
      <div className="mt-3 flex flex-col gap-1.5" role="radiogroup" aria-label="Ride options">
        {quote.options.map((o) => {
          const on = o.product === product;
          return (
            <button
              key={o.product}
              type="button"
              role="radio"
              aria-checked={on}
              onClick={() => setProduct(o.product)}
              className={`flex items-center gap-3 rounded-[14px] border-2 px-3 pt-[10px] pb-[8px] text-left ${on ? "border-ash" : "border-transparent hover:bg-fog"}`}
            >
              <div className="min-w-0 flex-1">
                <div className="text-[16px] font-bold">
                  {productName[o.product]} <span className="text-[13px] font-normal text-slate">{seats[o.product]} seats</span>
                </div>
                <div className="text-[13px] text-slate">
                  {o.pickupEta < 0 ? "No cars close by right now" : `${minutes(Math.max(60, o.pickupEta))} away, drop-off ${clock(simNow + (Math.max(60, o.pickupEta) + quote.seconds) * 1000)}`}
                </div>
              </div>
              <div className="text-[17px] font-bold">{money(o.fare.total)}</div>
            </button>
          );
        })}
      </div>
      <div className="mt-3">
        <Button onClick={onRequest} disabled={busy}>
          {busy ? "Requesting..." : `Request ${productName[product]}`}
        </Button>
      </div>
    </div>
  );
}

function FareLines({ trip }: { trip: Trip }) {
  const f = trip.fare;
  const rows: [string, number][] = [
    ["Trip fare", f.base],
    ["Busy area pricing", f.surgeAmount],
    ["Sales tax", f.salesTax],
    ["Black Car Fund", f.blackCarFund],
    ["NYS congestion surcharge", f.congestion],
    ["Congestion relief zone toll", f.cbd],
  ];
  return (
    <dl className="mt-3 text-[14px]">
      {rows
        .filter(([, v]) => v > 0)
        .map(([k, v]) => (
          <div key={k} className="flex justify-between py-1">
            <dt className="text-slate">{k}</dt>
            <dd>{money(v)}</dd>
          </div>
        ))}
      <div className="mt-1 flex justify-between border-t border-[#e3e8ea] pt-2 text-[16px] font-bold">
        <dt>Total</dt>
        <dd>{money(f.total)}</dd>
      </div>
    </dl>
  );
}

function DriverCardBlock({ trip }: { trip: Trip }) {
  const d = trip.driver!;
  return (
    <div className="mt-3 flex items-center gap-3 rounded-[16px] bg-fog p-3">
      <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-full bg-ash text-[18px] font-bold text-white">{d.name[0]}</div>
      <div className="min-w-0 flex-1">
        <div className="text-[16px] font-bold">
          {d.name} <span className="text-[13px] font-normal text-slate">{d.rating.toFixed(2)} rating</span>
        </div>
        <div className="truncate text-[13px] text-slate">{d.car}</div>
      </div>
      <div className="shrink-0 rounded-[6px] border-2 border-ash bg-[#f7d84a] px-2 pt-[4px] pb-[2px] text-[14px] font-extrabold tracking-[0.04em]">{d.plate}</div>
    </div>
  );
}

function TripPanel({
  trip,
  simNow,
  onCancel,
  onDone,
  onRate,
}: {
  trip: Trip;
  simNow: number;
  onCancel: () => void;
  onDone: () => void;
  onRate: (n: number) => void;
}) {
  switch (trip.state) {
    case "searching":
    case "offering":
      return (
        <div>
          <h2 className="text-[22px] font-extrabold">Finding your car</h2>
          <p className="mt-1 text-[14px] text-slate">
            {trip.offers > 1 ? `Asked ${trip.offers} nearby drivers so far.` : "Matching you with the best nearby driver."} Pickup at {trip.pickup.name}.
          </p>
          <div className="mt-4">
            <Button kind="danger" onClick={onCancel}>
              Cancel request
            </Button>
          </div>
        </div>
      );
    case "accepted":
      return (
        <div>
          <h2 className="text-[22px] font-extrabold">
            {trip.driver?.name.split(" ")[0]} is {trip.eta > 0 ? minutes(trip.eta) : "a few minutes"} away
          </h2>
          <p className="mt-1 text-[14px] text-slate">
            {trip.b2b ? "Finishing a drop-off nearby first. " : ""}Meet at {trip.pickup.name}.
          </p>
          <DriverCardBlock trip={trip} />
          <div className="mt-3">
            <Button kind="danger" onClick={onCancel}>
              Cancel ride
            </Button>
          </div>
        </div>
      );
    case "arrived":
      return (
        <div>
          <h2 className="text-[22px] font-extrabold">Your car is here</h2>
          <p className="mt-1 text-[14px] text-slate">Look for plate {trip.driver?.plate} at {trip.pickup.name}.</p>
          <DriverCardBlock trip={trip} />
        </div>
      );
    case "on_trip":
      return (
        <div>
          <h2 className="text-[22px] font-extrabold">On the way to {trip.dropoff.name}</h2>
          <p className="mt-1 text-[14px] text-slate">Arriving around {clock(simNow + Math.max(0, trip.eta) * 1000)}</p>
          <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-fog">
            <div
              className="h-full rounded-full bg-sign transition-[width] duration-1000"
              style={{ width: `${Math.min(100, Math.max(4, 100 * (1 - trip.eta / Math.max(trip.seconds, 1))))}%` }}
            />
          </div>
          <DriverCardBlock trip={trip} />
        </div>
      );
    case "completed":
      return (
        <div>
          <h2 className="text-[22px] font-extrabold">You arrived</h2>
          <p className="mt-1 text-[14px] text-slate">
            {trip.dropoff.name}, {distance(trip.meters)} with {trip.driver?.name}
          </p>
          <FareLines trip={trip} />
          <div className="mt-4">
            <p className="mb-1 text-[14px] font-semibold">{trip.rating ? "Thanks for rating" : "How was your ride?"}</p>
            <Stars value={trip.rating ?? 0} onRate={(n) => !trip.rating && onRate(n)} />
          </div>
          <div className="mt-4">
            <Button kind="dark" onClick={onDone}>
              Done
            </Button>
          </div>
        </div>
      );
    default:
      return (
        <div>
          <h2 className="text-[22px] font-extrabold">{trip.cancelReason === "no_drivers" ? "No cars were free" : "Ride cancelled"}</h2>
          <p className="mt-1 text-[14px] text-slate">
            {trip.cancelReason === "no_drivers"
              ? "Every nearby driver was busy. Try again in a minute, or pick a spot on a bigger street."
              : trip.cancelFee
                ? `A ${money(trip.cancelFee)} fee applied because your driver was already on the way for a while.`
                : "You were not charged."}
          </p>
          <div className="mt-4">
            <Button kind="dark" onClick={onDone}>
              Book another ride
            </Button>
          </div>
        </div>
      );
  }
}
