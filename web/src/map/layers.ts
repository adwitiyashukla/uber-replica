import { IconLayer, PathLayer, ScatterplotLayer, TextLayer } from "@deck.gl/layers";
import { H3HexagonLayer, TripsLayer } from "@deck.gl/geo-layers";
import type { Fleet, Trail } from "../lib/live";
import type { SurgeCell } from "../lib/types";

function svg(body: string, w: number, h: number) {
  return {
    url: `data:image/svg+xml;charset=utf-8,${encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}">${body}</svg>`)}`,
    width: w,
    height: h,
  };
}

export const icons = {
  car: svg(
    '<rect x="9" y="3" width="22" height="42" rx="9" fill="#ffffff" stroke="#10202b" stroke-width="2.5"/><rect x="13" y="12" width="14" height="9" rx="3" fill="#10202b"/><rect x="13" y="30" width="14" height="6" rx="2.5" fill="#10202b"/>',
    40,
    48,
  ),
  myCar: svg(
    '<rect x="9" y="3" width="22" height="42" rx="9" fill="#0a6b45" stroke="#ffffff" stroke-width="3"/><rect x="13" y="12" width="14" height="9" rx="3" fill="#ffffff"/><rect x="13" y="30" width="14" height="6" rx="2.5" fill="#ffffff"/>',
    40,
    48,
  ),
  pickup: svg('<circle cx="18" cy="18" r="13" fill="#0a6b45" stroke="#ffffff" stroke-width="4"/><circle cx="18" cy="18" r="4.5" fill="#ffffff"/>', 36, 36),
  dropoff: svg('<rect x="5" y="5" width="26" height="26" rx="4" fill="#10202b" stroke="#ffffff" stroke-width="4"/><rect x="14" y="14" width="8" height="8" fill="#ffffff"/>', 36, 36),
};

export function carsLayer(id: string, fleet: Fleet, now: number, radius: number, maxPx: number, alpha = 1) {
  return new ScatterplotLayer({
    id,
    data: {
      length: fleet.count,
      attributes: {
        getPosition: { value: fleet.positions(now), size: 2 },
        getFillColor: { value: fleet.colors, size: 4, normalized: true },
      },
    },
    radiusUnits: "meters",
    getRadius: radius,
    radiusMinPixels: 1.6,
    radiusMaxPixels: maxPx,
    opacity: alpha,
    pickable: true,
  });
}

export function trailsLayer(fleet: Fleet, now: number) {
  return new TripsLayer<Trail>({
    id: "trails",
    data: fleet.trails,
    getPath: (d) => d.path as unknown as [number, number][],
    positionFormat: "XY",
    getTimestamps: (d) => d.timestamps,
    getColor: (d) => d.color,
    currentTime: fleet.trailTime(now),
    trailLength: 26,
    fadeTrail: true,
    widthMinPixels: 1.6,
    widthMaxPixels: 3,
    getWidth: 8,
    opacity: 0.45,
    capRounded: true,
    jointRounded: true,
  });
}

export function surgeLayer(id: string, cells: SurgeCell[], strong = 1) {
  return new H3HexagonLayer<SurgeCell>({
    id,
    data: cells,
    getHexagon: (d) => d.cell,
    getFillColor: (d) => [255, 77, 109, Math.min(120, (d.value - 1) * 140) * strong],
    getLineColor: (d) => [255, 120, 140, Math.min(180, 40 + (d.value - 1) * 140) * strong],
    lineWidthMinPixels: 1,
    stroked: true,
    filled: true,
    extruded: false,
    updateTriggers: { getFillColor: strong, getLineColor: strong },
  });
}

export function surgeLabels(id: string, cells: SurgeCell[], centers: Map<string, [number, number]>, dark: boolean) {
  return new TextLayer<SurgeCell>({
    id,
    data: cells.filter((c) => c.value >= 1.3 && centers.has(c.cell)),
    getPosition: (d) => centers.get(d.cell)!,
    getText: (d) => `${d.value.toFixed(1)}x`,
    getSize: 13,
    getColor: dark ? [255, 220, 226, 230] : [150, 20, 45, 255],
    fontFamily: "Overpass Variable, sans-serif",
    fontWeight: 800,
    outlineWidth: dark ? 0 : 3,
    outlineColor: [255, 255, 255, 220],
    fontSettings: { sdf: !dark },
  });
}

export function routeLayer(id: string, points: [number, number][] | null, color: [number, number, number], width = 5) {
  return new PathLayer<{ path: [number, number][] }>({
    id,
    data: points && points.length > 1 ? [{ path: points }] : [],
    getPath: (d) => d.path,
    getColor: color,
    getWidth: width,
    widthUnits: "pixels",
    capRounded: true,
    jointRounded: true,
  });
}

export function pinsLayer(id: string, pins: { kind: "pickup" | "dropoff"; at: [number, number] }[]) {
  return new IconLayer<{ kind: "pickup" | "dropoff"; at: [number, number] }>({
    id,
    data: pins,
    getPosition: (d) => d.at,
    getIcon: (d) => icons[d.kind],
    getSize: 30,
    sizeUnits: "pixels",
  });
}

export function carIconLayer(id: string, at: [number, number] | null, heading: number, mine: boolean) {
  return new IconLayer<{ at: [number, number] }>({
    id,
    data: at ? [{ at }] : [],
    getPosition: (d) => d.at,
    getIcon: () => (mine ? icons.myCar : icons.car),
    getAngle: -heading,
    getSize: mine ? 44 : 38,
    sizeUnits: "pixels",
    updateTriggers: { getAngle: heading },
  });
}

export function pulseLayer(id: string, at: [number, number] | null, now: number, color: [number, number, number]) {
  const t = (now % 1800) / 1800;
  return new ScatterplotLayer<{ at: [number, number] }>({
    id,
    data: at ? [{ at }] : [],
    getPosition: (d) => d.at,
    getRadius: 40 + t * 260,
    radiusUnits: "meters",
    getFillColor: [...color, Math.round(90 * (1 - t))],
    getLineColor: [...color, Math.round(220 * (1 - t))],
    stroked: true,
    lineWidthMinPixels: 1.5,
    updateTriggers: { getRadius: t, getFillColor: t, getLineColor: t },
  });
}

export interface Ping {
  at: [number, number];
  born: number;
  color: [number, number, number];
}

export function pingsLayer(pings: Ping[], now: number) {
  return new ScatterplotLayer<Ping>({
    id: "pings",
    data: pings,
    getPosition: (d) => d.at,
    getRadius: (d) => 30 + Math.min(1, (now - d.born) / 2400) * 420,
    radiusUnits: "meters",
    filled: false,
    stroked: true,
    getLineColor: (d) => [...d.color, Math.round(230 * (1 - Math.min(1, (now - d.born) / 2400)))],
    lineWidthMinPixels: 1.5,
    updateTriggers: { getRadius: now, getLineColor: now },
  });
}
