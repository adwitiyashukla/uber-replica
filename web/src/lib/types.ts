export type Product = "standard" | "comfort" | "xl";
export type TripState = "searching" | "offering" | "accepted" | "arrived" | "on_trip" | "completed" | "cancelled";
export type DriverState = "offline" | "idle" | "offered" | "en_route" | "at_pickup" | "on_trip";

export interface Point {
  lat: number;
  lng: number;
}

export interface Place {
  name: string;
  zone: number;
  point: Point;
}

export interface Fare {
  base: number;
  surge: number;
  surgeAmount: number;
  subtotal: number;
  salesTax: number;
  blackCarFund: number;
  congestion: number;
  cbd: number;
  total: number;
}

export interface DriverCard {
  id: string;
  num: number;
  name: string;
  car: string;
  plate: string;
  product: Product;
  rating: number;
  state: DriverState;
  lat: number;
  lng: number;
  heading: number;
  simulated: boolean;
}

export interface Trip {
  id: string;
  riderId: string;
  simulated: boolean;
  product: Product;
  state: TripState;
  pickup: Place;
  dropoff: Place;
  fare: Fare;
  driverPay: number;
  meters: number;
  seconds: number;
  requestedAt: number;
  acceptedAt?: number;
  arrivedAt?: number;
  startedAt?: number;
  endedAt?: number;
  driver?: DriverCard;
  eta: number;
  b2b: boolean;
  offers: number;
  cancelReason?: string;
  cancelFee?: number;
  rating?: number;
}

export interface Offer {
  id: string;
  tripId: string;
  driverId: string;
  riderId: string;
  sentAt: number;
  expiresAt: number;
  eta: number;
  b2b: boolean;
  product: Product;
  pickup: Place;
  dropoff: Place;
  earnings: number;
  fare: number;
  surge: number;
  tripSeconds: number;
  tripMeters: number;
}

export interface DriverView extends DriverCard {
  trip?: Trip;
  next?: Trip;
  offer?: Offer;
  earnings: number;
  trips: number;
  onlineMs: number;
  goOffline: boolean;
}

export interface QuoteOption {
  product: Product;
  fare: Fare;
  driverPay: number;
  pickupEta: number;
  token: string;
  expiresAt: number;
}

export interface Quote {
  pickup: Place;
  dropoff: Place;
  meters: number;
  seconds: number;
  surge: number;
  options: QuoteOption[];
}

export interface Step {
  text: string;
  meters: number;
  lat: number;
  lng: number;
}

export interface RouteView {
  driverId: string;
  tripId?: string;
  purpose: "pickup" | "dropoff";
  points: [number, number][];
  steps: Step[];
  meters: number;
  seconds: number;
}

export interface SeriesPoint {
  at: number;
  requests: number;
  completed: number;
  waitP50: number;
  online: number;
  busy: number;
  surge: number;
}

export interface Totals {
  requests: number;
  accepted: number;
  completed: number;
  cancelled: number;
  noDriver: number;
  riderCancel: number;
  offers: number;
  declined: number;
  expired: number;
}

export interface Stats {
  at: number;
  online: number;
  idle: number;
  offered: number;
  busy: number;
  open: number;
  requests60: number;
  completed60: number;
  cancelled60: number;
  noDriver60: number;
  waitP50: number;
  waitP90: number;
  matchP50: number;
  fulfilled: number;
  utilization: number;
  surgeCells: number;
  surgeMax: number;
  surgeMean: number;
  revenue60: number;
  driverPay60: number;
  policy: "batch" | "greedy";
  backToBack: boolean;
  totals: Totals;
  series: SeriesPoint[] | null;
  sim: { sessions: number; lost: number; requested: number; spawned: number; retired: number; riderCancels: number };
  simOnline: number;
  speed: number;
  clients: number;
  bytesSent: number;
  storeWritten: number;
  storeDropped: number;
}

export interface FeedItem {
  at: number;
  kind: string;
  text: string;
  lat: number;
  lng: number;
  human: boolean;
}

export interface SurgeCell {
  cell: string;
  value: number;
}

export interface RealHour {
  hour: number;
  requests: number;
  wait_p50: number;
  wait_p90: number;
  trip_p50: number;
  trip_p90: number;
  fare_p50: number;
}

export interface Meta {
  replayDay: string;
  month: string;
  tripsDay: number;
  tripsMonth: number;
  center: [number, number];
  bounds: [number, number, number, number];
  products: { id: Product; name: string; seats: number; multiplier: number }[];
  real: { wait_p50: number; wait_p90: number; trip_p50: number; fare_p50: number; hours: RealHour[] };
  fare: { base: number; per_mile: number; per_minute: number; min: number; fit: number };
  clock: { now: number; speed: number; dayOffset: number };
  policy: "batch" | "greedy";
  backToBack: boolean;
  opsControls: boolean;
  opsResetMin: number;
  graph: { nodes: number; edges: number };
  surgeRes: number;
  frameRes: number;
  mapStyle: string;
  mapStyleLight: string;
  places: number;
}

export interface PlaceHit {
  name: string;
  kind: string;
  lat: number;
  lng: number;
}
