export type LngLat = [number, number];

export function project(points: LngLat[], at: LngLat): { seg: number; u: number } {
  const k = Math.cos((at[1] * Math.PI) / 180);
  let best = { seg: 0, u: 0 };
  let bd = Infinity;
  for (let i = 0; i + 1 < points.length; i++) {
    const ax = points[i][0] * k;
    const ay = points[i][1];
    const dx = points[i + 1][0] * k - ax;
    const dy = points[i + 1][1] - ay;
    const len = dx * dx + dy * dy;
    const u = len > 0 ? Math.max(0, Math.min(1, ((at[0] * k - ax) * dx + (at[1] - ay) * dy) / len)) : 0;
    const ex = ax + u * dx - at[0] * k;
    const ey = ay + u * dy - at[1];
    const d = ex * ex + ey * ey;
    if (d < bd) {
      bd = d;
      best = { seg: i, u };
    }
  }
  return best;
}

export function ahead(points: LngLat[], at: LngLat): LngLat[] {
  if (points.length < 2) return points;
  const { seg, u } = project(points, at);
  return [at, ...points.slice(u > 0 ? seg + 1 : seg)];
}

export function meters(a: LngLat, b: LngLat): number {
  const k = Math.cos((a[1] * Math.PI) / 180);
  return Math.hypot((a[0] - b[0]) * k, a[1] - b[1]) * 111320;
}
