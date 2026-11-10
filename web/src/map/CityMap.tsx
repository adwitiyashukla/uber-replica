import maplibregl, { type StyleSpecification } from "maplibre-gl";
import { MapboxOverlay } from "@deck.gl/mapbox";
import type { Layer } from "@deck.gl/core";
import { PathLayer } from "@deck.gl/layers";
import { useEffect, useRef } from "react";
import { api } from "../lib/api";

export type BBox = [number, number, number, number];

interface Props {
  styleUrl: string;
  theme: "dark" | "light";
  center: [number, number];
  zoom: number;
  pitch?: number;
  minZoom?: number;
  showGraph?: boolean;
  layers: (now: number, zoom: number) => Layer[];
  onView?: (bbox: BBox, zoom: number) => void;
  onClick?: (lng: number, lat: number) => void;
  onMap?: (map: maplibregl.Map) => void;
  tooltip?: (layerId: string, index: number) => string | null;
  bearing?: number;
  padding?: { top: number; bottom: number; left: number; right: number };
  label: string;
  className?: string;
}

const palette = {
  dark: { ground: "#0e1822", water: "#08121b", minor: [40, 64, 84], major: [63, 92, 116] },
  light: { ground: "#eef2f3", water: "#c9dde8", minor: [198, 207, 213], major: [168, 181, 191] },
};

function blankStyle(theme: "dark" | "light"): StyleSpecification {
  return {
    version: 8,
    sources: {},
    layers: [{ id: "ground", type: "background", paint: { "background-color": palette[theme].ground } }],
  };
}

function tune(map: maplibregl.Map, theme: "dark" | "light") {
  const p = palette[theme];
  for (const layer of map.getStyle().layers ?? []) {
    try {
      if (layer.type === "background") map.setPaintProperty(layer.id, "background-color", p.ground);
      if (layer.type === "fill" && /water/.test(layer.id)) map.setPaintProperty(layer.id, "fill-color", p.water);
    } catch {
      continue;
    }
  }
}

type Roads = { path: number[]; cls: number }[];

let roadsCache: Promise<Roads> | null = null;

function loadRoads(): Promise<Roads> {
  roadsCache ??= api.roads().then((r) => r.lines.map((path, i) => ({ path, cls: r.classes[i] })));
  return roadsCache;
}

export function CityMap(props: Props) {
  const el = useRef<HTMLDivElement>(null);
  const latest = useRef(props);
  latest.current = props;

  useEffect(() => {
    const p = latest.current;
    const map = new maplibregl.Map({
      container: el.current!,
      style: p.styleUrl,
      center: p.center,
      zoom: p.zoom,
      pitch: p.pitch ?? 0,
      minZoom: p.minZoom ?? 10,
      maxZoom: 18,
      dragRotate: false,
      pitchWithRotate: false,
      attributionControl: { compact: true },
      maxBounds: [
        [-74.35, 40.5],
        [-73.6, 41.05],
      ],
    });
    map.touchZoomRotate.disableRotation();
    if (p.padding) map.setPadding(p.padding);
    if (p.bearing) map.setBearing(p.bearing);
    const overlay = new MapboxOverlay({
      interleaved: false,
      layers: [],
      getTooltip: (info) => {
        const fn = latest.current.tooltip;
        if (!fn || !info.layer || info.index < 0) return null;
        const text = fn(info.layer.id, info.index);
        return text
          ? {
              text,
              style: {
                background: "#10202b",
                color: "#eaf0f2",
                fontFamily: "Overpass Variable, sans-serif",
                fontSize: "12px",
                borderRadius: "8px",
                padding: "6px 8px",
              },
            }
          : null;
      },
    });
    map.addControl(overlay as unknown as maplibregl.IControl);
    let styled = false;
    let fallback = false;
    let roads: PathLayer | null = null;
    const theme = p.theme;
    map.getContainer().style.background = palette[theme].ground;

    const showRoads = () => {
      loadRoads().then((data) => {
        if (styled && !latest.current.showGraph) return;
        const c = palette[theme];
        roads = new PathLayer({
          id: "road-graph",
          data,
          getPath: (d: { path: number[] }) => d.path as unknown as [number, number][],
          positionFormat: "XY",
          getColor: (d: { cls: number }) => (d.cls <= 2 ? c.major : c.minor) as [number, number, number],
          getWidth: (d: { cls: number }) => (d.cls <= 1 ? 3 : d.cls <= 3 ? 2 : 1),
          widthUnits: "pixels",
          capRounded: true,
        });
      });
    };
    const useFallback = () => {
      if (styled || fallback) return;
      fallback = true;
      map.setStyle(blankStyle(theme));
      map.getContainer().dataset.fallback = "true";
      showRoads();
    };
    const timer = window.setTimeout(showRoads, 8000);
    map.on("style.load", () => {
      if (fallback) return;
      styled = true;
      window.clearTimeout(timer);
      tune(map, theme);
      if (latest.current.showGraph) showRoads();
      else roads = null;
    });
    map.on("error", () => {
      if (!styled) useFallback();
    });
    const emitView = () => {
      const b = map.getBounds();
      latest.current.onView?.([b.getWest(), b.getSouth(), b.getEast(), b.getNorth()], map.getZoom());
    };
    map.on("moveend", emitView);
    map.on("load", emitView);
    window.setTimeout(emitView, 300);
    map.on("click", (e) => latest.current.onClick?.(e.lngLat.lng, e.lngLat.lat));
    latest.current.onMap?.(map);

    let raf = 0;
    const loop = () => {
      const now = performance.now();
      const layers = latest.current.layers(now, map.getZoom());
      overlay.setProps({ layers: roads ? [roads, ...layers] : layers });
      raf = requestAnimationFrame(loop);
    };
    raf = requestAnimationFrame(loop);
    const resize = new ResizeObserver(() => map.resize());
    resize.observe(el.current!);
    return () => {
      cancelAnimationFrame(raf);
      window.clearTimeout(timer);
      resize.disconnect();
      map.remove();
    };
  }, []);

  return (
    <div className={`${props.theme === "light" ? "light-map" : ""} ${props.className ?? ""}`}>
      <div ref={el} role="application" aria-label={props.label} className="h-full w-full" />
    </div>
  );
}
