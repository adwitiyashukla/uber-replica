export function money(cents: number) {
  return `$${(cents / 100).toFixed(2)}`;
}

export function minutes(seconds: number) {
  const m = Math.round(seconds / 60);
  return m <= 1 ? "1 min" : `${m} min`;
}

export function clock(simMs: number) {
  const s = Math.floor(((simMs / 1000) % 86400 + 86400) % 86400);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const suffix = h < 12 ? "AM" : "PM";
  const hh = h % 12 === 0 ? 12 : h % 12;
  return `${hh}:${String(m).padStart(2, "0")} ${suffix}`;
}

export function hourOf(simMs: number) {
  return Math.floor((((simMs / 1000) % 86400) + 86400) % 86400 / 3600);
}

export function day(iso: string) {
  const d = new Date(`${iso}T12:00:00`);
  return d.toLocaleDateString("en-US", { weekday: "long", month: "long", day: "numeric", year: "numeric" });
}

export function distance(meters: number) {
  const miles = meters / 1609.344;
  if (miles < 0.1) return `${Math.max(50, Math.round((meters * 3.28084) / 50) * 50)} ft`;
  return `${miles.toFixed(1)} mi`;
}

export function compact(n: number) {
  if (n >= 10000) return `${(n / 1000).toFixed(0)}k`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(Math.round(n));
}

export function pct(x: number) {
  return `${Math.round(x * 100)}%`;
}

export const productName: Record<string, string> = { standard: "Standard", comfort: "Comfort", xl: "XL" };
