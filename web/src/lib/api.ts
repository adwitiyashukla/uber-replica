import type { DriverCard, DriverView, Meta, PlaceHit, Point, Product, Quote, Trip } from "./types";

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(data.error ?? `Request failed with ${res.status}`, res.status);
  return data as T;
}

export const api = {
  meta: () => call<Meta>("GET", "/api/meta"),
  places: (q: string, near?: Point) =>
    call<{ places: PlaceHit[] }>(
      "GET",
      `/api/places?q=${encodeURIComponent(q)}${near ? `&lat=${near.lat}&lng=${near.lng}` : ""}`,
    ),
  quote: (riderId: string, from: Point, to: Point, fromName = "", toName = "") =>
    call<{ quote: Quote; route: [number, number][] }>("POST", "/api/quote", { riderId, from, to, fromName, toName }),
  request: (riderId: string, token: string, ref: string) => call<Trip>("POST", "/api/rides", { riderId, token, ref }),
  cancel: (riderId: string, tripId: string) => call<Trip>("POST", `/api/rides/${tripId}/cancel`, { riderId }),
  rate: (riderId: string, tripId: string, stars: number) =>
    call<{ ok: boolean }>("POST", `/api/rides/${tripId}/rate`, { riderId, stars }),
  active: (riderId: string) => call<Trip | undefined>("GET", `/api/riders/${riderId}/active`),
  driver: (id: string) => call<DriverView | undefined>("GET", `/api/drivers/${id}`),
  online: (id: string, product: Product) => call<DriverView>("POST", `/api/drivers/${id}/online`, { product }),
  offline: (id: string) => call<DriverView>("POST", `/api/drivers/${id}/offline`, {}),
  accept: (id: string, offerId: string) => call<DriverView>("POST", `/api/drivers/${id}/accept`, { offerId }),
  decline: (id: string, offerId: string) => call<DriverView>("POST", `/api/drivers/${id}/decline`, { offerId }),
  start: (id: string, tripId: string) => call<DriverView>("POST", `/api/drivers/${id}/start`, { tripId }),
  complete: (id: string, tripId: string) => call<DriverView>("POST", `/api/drivers/${id}/complete`, { tripId }),
  car: (num: number) => call<DriverCard>("GET", `/api/cars/${num}`),
  ops: (body: { policy?: string; backToBack?: boolean; speed?: number }) => call<{ ok: boolean }>("POST", "/api/ops", body),
  roads: () => call<{ lines: number[][]; classes: number[] }>("GET", "/api/roads"),
};

const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789";

function randomId(prefix: string): string {
  const bytes = crypto.getRandomValues(new Uint8Array(12));
  let s = "";
  for (const b of bytes) s += alphabet[b % alphabet.length];
  return `${prefix}-${s}`;
}

export function stableId(kind: "rider" | "driver"): string {
  const key = `crosstown.${kind}`;
  try {
    const saved = localStorage.getItem(key);
    if (saved && /^[RH]-[a-z0-9]{6,24}$/.test(saved)) return saved;
  } catch {
    return randomId(kind === "rider" ? "R" : "H");
  }
  const id = randomId(kind === "rider" ? "R" : "H");
  try {
    localStorage.setItem(key, id);
  } catch {
    return id;
  }
  return id;
}

export function requestRef(): string {
  return randomId("ref").slice(4);
}
