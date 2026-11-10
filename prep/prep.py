import argparse
import gzip
import io
import json
import math
import struct
import sys
import time
import unicodedata
import zipfile
from collections import defaultdict
from pathlib import Path
from urllib.parse import urlencode
from urllib.request import Request, urlopen

import numpy as np
import pandas as pd
import pyarrow as pa
import pyarrow.compute as pc
import pyarrow.parquet as pq
import shapefile
import shapely
from pyproj import Transformer
from scipy.sparse import csr_matrix
from scipy.sparse.csgraph import connected_components, dijkstra

BBOX = (40.6960, -74.0250, 40.8830, -73.9050)
TLC = "https://d37ci6vzurychx.cloudfront.net"
OVERPASS = [
    "https://overpass-api.de/api/interpreter",
    "https://overpass.kumi.systems/api/interpreter",
    "https://overpass.private.coffee/api/interpreter",
]
DRIVABLE = (
    "motorway|motorway_link|trunk|trunk_link|primary|primary_link|secondary|secondary_link|"
    "tertiary|tertiary_link|unclassified|residential|living_street"
)
RANK = {
    "motorway": 0, "trunk": 1, "primary": 2, "secondary": 3,
    "tertiary": 4, "unclassified": 5, "residential": 6, "living_street": 7,
}
DEFAULT_MPH = {"motorway": 45, "trunk": 35, "living_street": 10}
SPEED_FACTOR = {
    "motorway": 0.9, "motorway_link": 0.75, "trunk": 0.8, "trunk_link": 0.7,
    "primary": 0.55, "secondary": 0.55,
}
ABBR = {
    "west": "W", "east": "E", "north": "N", "south": "S", "street": "St", "avenue": "Ave",
    "boulevard": "Blvd", "place": "Pl", "drive": "Dr", "road": "Rd", "parkway": "Pkwy",
    "highway": "Hwy", "square": "Sq", "lane": "Ln", "terrace": "Ter", "plaza": "Plz",
    "expressway": "Expy",
}
POI_WEIGHT = {
    "station": 6, "attraction": 6, "museum": 5, "zoo": 5, "viewpoint": 4, "park": 4,
    "stadium": 5, "hotel": 3, "theatre": 4, "cinema": 3, "concert_hall": 4, "events_venue": 3,
    "university": 4, "college": 3, "hospital": 4, "library": 3, "department_store": 3, "mall": 3,
    "garden": 3, "gallery": 2, "arts_centre": 3, "historic": 2, "office": 2, "building": 2,
    "restaurant": 1, "cafe": 1, "bar": 1, "pub": 1, "nightclub": 1, "artwork": 1,
}

TO_FT = Transformer.from_crs("EPSG:4326", "EPSG:2263", always_xy=True)
TO_LL = Transformer.from_crs("EPSG:2263", "EPSG:4326", always_xy=True)


def roads_query():
    s, w, n, e = BBOX
    return (
        "[out:json][timeout:300];"
        f'way["highway"~"^({DRIVABLE})$"]["area"!="yes"]["access"!~"^(private|no)$"]'
        f'["motor_vehicle"!~"^(private|no)$"]["motorcar"!~"^(private|no)$"]({s},{w},{n},{e});'
        "(._;>;);out body qt;"
    )


def places_query():
    s, w, n, e = BBOX
    b = f"({s},{w},{n},{e})"
    parts = [
        f'nwr["name"]["tourism"~"^(attraction|museum|gallery|hotel|viewpoint|zoo|artwork|theme_park)$"]{b};',
        f'nwr["name"]["amenity"~"^(theatre|cinema|university|college|hospital|library|arts_centre|restaurant|cafe|bar|pub|nightclub|marketplace|townhall|courthouse|community_centre|events_venue|conference_centre|concert_hall)$"]{b};',
        f'nwr["name"]["leisure"~"^(park|stadium|garden|sports_centre|marina)$"]{b};',
        f'nwr["name"]["railway"="station"]{b};',
        f'nwr["name"]["shop"~"^(department_store|mall)$"]{b};',
        f'nwr["name"]["historic"]{b};',
        f'nwr["name"]["building"~"^(office|commercial|skyscraper|stadium|train_station|transportation|museum|civic|government|cathedral|church)$"]{b};',
    ]
    return "[out:json][timeout:300];(" + "".join(parts) + ");out center tags qt;"


def download(url, dest, data=None):
    req = Request(url, data=data, headers={"User-Agent": "uber-replica-prep/0.1"})
    with urlopen(req, timeout=900) as r, open(dest, "wb") as f:
        while True:
            chunk = r.read(1 << 20)
            if not chunk:
                break
            f.write(chunk)


def overpass(query, dest):
    body = urlencode({"data": query}).encode()
    for attempt in range(8):
        url = OVERPASS[attempt % len(OVERPASS)]
        try:
            download(url, dest, body)
            with open(dest, "rb") as f:
                if f.read(1) == b"{" and dest.stat().st_size > 100_000:
                    return
            print(f"  {url} answered with an error page, retrying")
        except OSError as err:
            print(f"  {url} failed: {err}")
        time.sleep(15)
    sys.exit(f"could not download {dest.name} from overpass")


def fetch(args):
    raw = Path(args.raw)
    raw.mkdir(parents=True, exist_ok=True)
    jobs = [
        (f"{TLC}/trip-data/fhvhv_tripdata_{args.month}.parquet", raw / f"fhvhv_tripdata_{args.month}.parquet"),
        (f"{TLC}/misc/taxi_zones.zip", raw / "taxi_zones.zip"),
        (f"{TLC}/misc/taxi_zone_lookup.csv", raw / "taxi_zone_lookup.csv"),
    ]
    for url, dest in jobs:
        if dest.exists() and dest.stat().st_size > 0:
            print(f"have {dest.name}")
            continue
        print(f"downloading {url}")
        download(url, dest)
    for name, query in (("osm_roads.json", roads_query()), ("osm_places.json", places_query())):
        dest = raw / name
        if dest.exists() and dest.stat().st_size > 100_000:
            print(f"have {name}")
            continue
        print(f"querying overpass for {name}")
        overpass(query, dest)


def ascii_name(name):
    for a, b in (("\u2013", "-"), ("\u2014", "-"), ("\u2018", "'"), ("\u2019", "'"), ("\u201c", '"'), ("\u201d", '"'), ("\u2026", "...")):
        name = name.replace(a, b)
    name = unicodedata.normalize("NFKD", name).encode("ascii", "ignore").decode("ascii")
    return " ".join(name.split())


def abbreviate(name):
    name = ascii_name(name)
    out = []
    for tok in name.split():
        out.append(ABBR.get(tok.lower(), tok))
    return " ".join(out)


def haversine(lat1, lon1, lat2, lon2):
    p1, p2 = math.radians(lat1), math.radians(lat2)
    dp = p2 - p1
    dl = math.radians(lon2 - lon1)
    a = math.sin(dp / 2) ** 2 + math.cos(p1) * math.cos(p2) * math.sin(dl / 2) ** 2
    return 2 * 6371008.8 * math.asin(math.sqrt(a))


def parse_mph(tag):
    if not tag:
        return None
    t = tag.strip().lower()
    try:
        if t.endswith("mph"):
            return float(t[:-3])
        return float(t) * 0.621371
    except ValueError:
        return None


def way_speed(tags):
    hw = tags["highway"]
    base = hw.replace("_link", "")
    limit = parse_mph(tags.get("maxspeed")) or DEFAULT_MPH.get(base, 25)
    limit = min(max(limit, 5), 55)
    return limit * SPEED_FACTOR.get(hw, 0.5) * 0.44704


def way_direction(tags):
    ow = tags.get("oneway")
    if ow in ("yes", "true", "1"):
        return 1
    if ow == "-1":
        return -1
    if ow in ("reversible", "alternating"):
        return 0
    if ow == "no":
        return 2
    if tags["highway"] in ("motorway", "motorway_link") or tags.get("junction") in ("roundabout", "circular"):
        return 1
    return 2


def load_zones(raw):
    z = zipfile.ZipFile(raw / "taxi_zones.zip")
    files = {Path(n).suffix: n for n in z.namelist() if not n.endswith("/")}
    reader = shapefile.Reader(
        shp=io.BytesIO(z.read(files[".shp"])),
        dbf=io.BytesIO(z.read(files[".dbf"])),
        shx=io.BytesIO(z.read(files[".shx"])),
    )
    zones = {}
    for sr in reader.iterShapeRecords():
        rec = sr.record.as_dict()
        if rec["borough"] != "Manhattan":
            continue
        zid = int(rec["LocationID"])
        geom = shapely.geometry.shape(sr.shape.__geo_interface__).buffer(0)
        if zid in zones:
            zones[zid]["geom"] = zones[zid]["geom"].union(geom)
        else:
            zones[zid] = {"name": ascii_name(rec["zone"]), "geom": geom}
    return zones


def assign_zones(lat, lon, zones, buffer_ft):
    x, y = TO_FT.transform(np.asarray(lon), np.asarray(lat))
    zid = np.zeros(len(x), dtype=np.int32)
    ids = sorted(zones)
    for z in ids:
        g = zones[z]["geom"]
        shapely.prepare(g)
        inside = shapely.contains_xy(g, x, y)
        zid[(zid == 0) & inside] = z
    rest = np.where(zid == 0)[0]
    if len(rest) and buffer_ft > 0:
        tree = shapely.STRtree([zones[z]["geom"] for z in ids])
        hits = tree.query_nearest(shapely.points(x[rest], y[rest]), max_distance=buffer_ft)
        zid[rest[hits[0]]] = np.asarray(ids, dtype=np.int32)[hits[1]]
    return zid


def build_graph(raw, zones):
    data = json.loads((raw / "osm_roads.json").read_text(encoding="utf-8"))
    coords = {}
    ways = []
    for e in data["elements"]:
        if e["type"] == "node":
            coords[e["id"]] = (e["lat"], e["lon"])
        elif e["type"] == "way":
            ways.append(e)
    osm_ids = np.fromiter(coords.keys(), dtype=np.int64)
    lat = np.array([coords[i][0] for i in osm_ids])
    lon = np.array([coords[i][1] for i in osm_ids])
    zone_of = dict(zip(osm_ids.tolist(), assign_zones(lat, lon, zones, 200.0).tolist()))

    edges = {}
    for w in ways:
        tags = w.get("tags", {})
        direction = way_direction(tags)
        if direction == 0:
            continue
        speed = way_speed(tags)
        name = abbreviate(tags.get("name") or tags.get("ref") or "")
        rank = RANK.get(tags["highway"].replace("_link", ""), 8)
        nodes = w["nodes"]
        for a, b in zip(nodes, nodes[1:]):
            if a == b or zone_of.get(a, 0) == 0 or zone_of.get(b, 0) == 0:
                continue
            length = haversine(*coords[a], *coords[b])
            pairs = []
            if direction in (1, 2):
                pairs.append((a, b))
            if direction in (-1, 2):
                pairs.append((b, a))
            for u, v in pairs:
                t = length / speed
                old = edges.get((u, v))
                if old is None or t < old[1]:
                    edges[(u, v)] = (length, t, name, rank)

    nodes = sorted({u for u, _ in edges} | {v for _, v in edges})
    index = {n: i for i, n in enumerate(nodes)}
    rows = [index[u] for u, _ in edges]
    cols = [index[v] for _, v in edges]
    m = csr_matrix((np.ones(len(rows)), (rows, cols)), shape=(len(nodes), len(nodes)))
    _, labels = connected_components(m, directed=True, connection="strong")
    big = np.bincount(labels).argmax()
    keep = {n for n, lab in zip(nodes, labels) if lab == big}
    edges = {k: v for k, v in edges.items() if k[0] in keep and k[1] in keep}
    print(f"  osm nodes {len(coords)}, segments in largest component {len(edges)}, nodes {len(keep)}")

    out_adj = defaultdict(list)
    in_adj = defaultdict(list)
    for (u, v) in edges:
        out_adj[u].append(v)
        in_adj[v].append(u)

    def passthrough(n):
        outs, ins = out_adj[n], in_adj[n]
        if len(outs) == 1 and len(ins) == 1 and outs[0] != ins[0]:
            return True
        return len(outs) == 2 and len(ins) == 2 and set(outs) == set(ins) and len(set(outs)) == 2

    kept = {n for n in keep if not passthrough(n)}
    if not kept:
        sys.exit("graph has no junctions")
    chains = {}
    for u in sorted(kept):
        for first in out_adj[u]:
            length, t, name, rank = 0.0, 0.0, "", 9
            pts = []
            prev, cur = u, first
            steps = 0
            while True:
                el, et, en, er = edges[(prev, cur)]
                length += el
                t += et
                name = name or en
                rank = min(rank, er)
                if cur in kept:
                    break
                nxt = [x for x in out_adj[cur] if x != prev]
                if len(nxt) != 1 or steps > 100000:
                    kept.add(cur)
                    break
                pts.append(coords[cur])
                prev, cur = cur, nxt[0]
                steps += 1
            if cur == u:
                continue
            old = chains.get((u, cur))
            if old is None or t < old[1]:
                chains[(u, cur)] = (length, t, name, rank, pts)

    order = sorted(kept)
    nidx = {n: i for i, n in enumerate(order)}
    chains = {k: v for k, v in chains.items() if k[0] in nidx and k[1] in nidx}
    rows = [nidx[u] for u, _ in chains]
    cols = [nidx[v] for _, v in chains]
    m = csr_matrix((np.ones(len(rows)), (rows, cols)), shape=(len(order), len(order)))
    ncomp, labels = connected_components(m, directed=True, connection="strong")
    if ncomp != 1:
        big = np.bincount(labels).argmax()
        good = {n for n, lab in zip(order, labels) if lab == big}
        order = [n for n in order if n in good]
        nidx = {n: i for i, n in enumerate(order)}
        chains = {k: v for k, v in chains.items() if k[0] in nidx and k[1] in nidx}
    print(f"  contracted graph: {len(order)} nodes, {len(chains)} edges")

    g = {
        "lat": np.array([coords[n][0] for n in order]),
        "lon": np.array([coords[n][1] for n in order]),
        "zone": np.array([zone_of[n] for n in order], dtype=np.uint16),
        "edges": sorted(((nidx[u], nidx[v]) + chains[(u, v)] for (u, v) in chains), key=lambda e: (e[0], e[1])),
    }
    return g


def graph_csr(g, field):
    n = len(g["lat"])
    rows = [e[0] for e in g["edges"]]
    cols = [e[1] for e in g["edges"]]
    vals = [e[field] for e in g["edges"]]
    return csr_matrix((vals, (rows, cols)), shape=(n, n))


def write_graph(g, path):
    n = len(g["lat"])
    edges = g["edges"]
    names = sorted({e[4] for e in edges})
    name_idx = {s: i for i, s in enumerate(names)}
    first = np.zeros(n + 1, dtype=np.uint32)
    for e in edges:
        first[e[0] + 1] += 1
    first = np.cumsum(first).astype(np.uint32)
    geo_first = [0]
    pts = []
    for e in edges:
        pts.extend(e[6])
        geo_first.append(len(pts))
    buf = io.BytesIO()
    buf.write(b"XTG1")
    buf.write(struct.pack("<IIII", n, len(edges), len(pts), len(names)))
    buf.write(np.round(g["lat"] * 1e6).astype("<i4").tobytes())
    buf.write(np.round(g["lon"] * 1e6).astype("<i4").tobytes())
    buf.write(g["zone"].astype("<u2").tobytes())
    buf.write(first.astype("<u4").tobytes())
    buf.write(np.array([e[1] for e in edges], dtype="<u4").tobytes())
    buf.write(np.array([e[2] for e in edges], dtype="<f4").tobytes())
    buf.write(np.array([e[3] for e in edges], dtype="<f4").tobytes())
    buf.write(np.array([name_idx[e[4]] for e in edges], dtype="<u4").tobytes())
    buf.write(np.array([e[5] for e in edges], dtype="<u1").tobytes())
    buf.write(np.array(geo_first, dtype="<u4").tobytes())
    buf.write(np.round(np.array([p[0] for p in pts]) * 1e6).astype("<i4").tobytes())
    buf.write(np.round(np.array([p[1] for p in pts]) * 1e6).astype("<i4").tobytes())
    for s in names:
        b = s.encode("utf-8")
        buf.write(struct.pack("<H", len(b)))
        buf.write(b)
    write_gz(path, buf.getvalue())


def write_gz(path, data):
    with open(path, "wb") as f:
        with gzip.GzipFile(filename="", mode="wb", fileobj=f, mtime=0, compresslevel=9) as gz:
            gz.write(data)


def load_trips(raw, month, zones):
    man = pa.array(sorted(zones), type=pa.int32())
    cols = [
        "hvfhs_license_num", "request_datetime", "on_scene_datetime", "pickup_datetime", "dropoff_datetime",
        "PULocationID", "DOLocationID", "trip_miles", "trip_time", "base_passenger_fare", "bcf", "sales_tax",
        "congestion_surcharge", "cbd_congestion_fee", "driver_pay", "shared_request_flag",
    ]
    f = pq.ParquetFile(raw / f"fhvhv_tripdata_{month}.parquet")
    parts = []
    for i in range(f.num_row_groups):
        t = f.read_row_group(i, columns=cols)
        keep = pc.and_(
            pc.equal(t["hvfhs_license_num"], "HV0003"),
            pc.and_(pc.is_in(t["PULocationID"], value_set=man), pc.is_in(t["DOLocationID"], value_set=man)),
        )
        parts.append(t.filter(keep).drop_columns(["hvfhs_license_num"]))
    df = pa.concat_tables(parts).to_pandas()
    df["wait"] = (df["on_scene_datetime"] - df["request_datetime"]).dt.total_seconds()
    df["hour"] = df["request_datetime"].dt.hour
    df["date"] = df["request_datetime"].dt.strftime("%Y-%m-%d")
    df["weekday"] = df["request_datetime"].dt.dayofweek
    return df[df["date"].str.startswith(month)].reset_index(drop=True)


def fit_median(df, target, iters=15):
    X = np.column_stack([np.ones(len(df)), df["trip_miles"].to_numpy(), df["trip_time"].to_numpy() / 60.0])
    y = df[target].to_numpy()
    coef, *_ = np.linalg.lstsq(X, y, rcond=None)
    for _ in range(iters):
        w = np.sqrt(1.0 / np.maximum(np.abs(y - X @ coef), 0.05))
        coef, *_ = np.linalg.lstsq(X * w[:, None], y * w, rcond=None)
    fit = 1 - np.sum(np.abs(y - X @ coef)) / np.sum(np.abs(y - np.median(y)))
    return [round(float(c), 4) for c in coef], round(float(fit), 4)


def pct(x, q):
    return round(float(np.percentile(x, q)), 2) if len(x) else 0.0


def calibrate(df, g, day, seed):
    rng = np.random.default_rng(seed)
    clean = df[
        (df["shared_request_flag"] == "N") & (df["base_passenger_fare"] > 2) & (df["trip_time"].between(120, 7200))
        & (df["trip_miles"].between(0.2, 30)) & (df["driver_pay"] > 0)
    ]
    fare, fare_fit = fit_median(clean, "base_passenger_fare")
    pay, pay_fit = fit_median(clean, "driver_pay")
    base = clean["base_passenger_fare"]
    min_fare = round(float(np.percentile(base, 5)), 2)
    sales_rate = round(float(np.median(clean["sales_tax"] / base)), 5)
    bcf_rate = round(float(np.median(clean["bcf"] / base)), 5)

    intra = df[df["PULocationID"] == df["DOLocationID"]]
    by_zone = intra.groupby("PULocationID").agg(cong=("congestion_surcharge", lambda s: (s > 0).mean()), cbd=("cbd_congestion_fee", lambda s: (s > 0).mean()))
    south96 = sorted(int(z) for z, r in by_zone.iterrows() if r["cong"] > 0.5)
    cbd = sorted(int(z) for z, r in by_zone.iterrows() if r["cbd"] > 0.5)

    zone_nodes = defaultdict(list)
    for i, z in enumerate(g["zone"].tolist()):
        zone_nodes[z].append(i)
    reps = {}
    flat = []
    for z in sorted(zone_nodes):
        pick = rng.choice(zone_nodes[z], size=min(8, len(zone_nodes[z])), replace=False)
        reps[z] = list(range(len(flat), len(flat) + len(pick)))
        flat.extend(int(p) for p in pick)
    times = dijkstra(graph_csr(g, 3), directed=True, indices=flat)
    meters = dijkstra(graph_csr(g, 2), directed=True, indices=flat)

    sample = df[(df["date"] == day) & df["trip_time"].between(60, 7200) & (df["trip_miles"] > 0.1)]
    sample = sample[sample["PULocationID"].isin(reps.keys()) & sample["DOLocationID"].isin(reps.keys())]
    factors, dist_ratio = [], []
    for h in range(24):
        s = sample[sample["hour"] == h]
        s = s.sample(n=min(4000, len(s)), random_state=seed + h)
        real_t, model_t, real_m, model_m = [], [], [], []
        for pu, do, tt, mi in zip(s["PULocationID"], s["DOLocationID"], s["trip_time"], s["trip_miles"]):
            r = reps[pu][rng.integers(len(reps[pu]))]
            dest = flat[reps[do][rng.integers(len(reps[do]))]]
            ff = times[r, dest]
            if not np.isfinite(ff) or ff < 60:
                continue
            real_t.append(tt)
            model_t.append(ff)
            real_m.append(mi * 1609.344)
            model_m.append(meters[r, dest])
        factors.append(round(float(np.median(real_t) / np.median(model_t)), 4))
        dist_ratio.append(round(float(np.median(real_m) / np.median(model_m)), 4))

    d = df[df["date"] == day]
    wait = d["wait"].clip(lower=0, upper=3600)
    hours = []
    for h in range(24):
        s = d[d["hour"] == h]
        w = wait[d["hour"] == h]
        hours.append({
            "hour": h, "requests": int(len(s)),
            "wait_p50": pct(w, 50), "wait_p90": pct(w, 90),
            "trip_p50": pct(s["trip_time"], 50), "trip_p90": pct(s["trip_time"], 90),
            "fare_p50": pct(s["base_passenger_fare"], 50),
        })
    weekday = pd.Timestamp(day).dayofweek
    others = df[(df["weekday"] == weekday) & (df["date"] != day)]
    ndays = max(others["date"].nunique(), 1)
    zh = others.groupby(["PULocationID", "hour"]).size() / ndays
    forecast = {}
    for z in sorted(zone_nodes):
        forecast[str(z)] = [round(float(zh.get((z, h), 0.0)), 2) for h in range(24)]

    return {
        "month": df["date"].min()[:7],
        "replay_day": day,
        "trips_month": int(len(df)),
        "trips_day": int(len(d)),
        "fare": {"base": fare[0], "per_mile": fare[1], "per_minute": fare[2], "min": min_fare, "fit": fare_fit},
        "driver_pay": {"base": pay[0], "per_mile": pay[1], "per_minute": pay[2], "fit": pay_fit},
        "fees": {"sales_tax_rate": sales_rate, "black_car_fund_rate": bcf_rate, "congestion_surcharge": 2.75, "cbd_fee": 1.5},
        "zones_south_of_96th": south96,
        "zones_cbd": cbd,
        "hour_factor": factors,
        "distance_ratio": dist_ratio,
        "real": {
            "wait_p50": pct(wait, 50), "wait_p90": pct(wait, 90), "wait_mean": round(float(wait.mean()), 2),
            "trip_p50": pct(d["trip_time"], 50), "trip_p90": pct(d["trip_time"], 90),
            "fare_p50": pct(d["base_passenger_fare"], 50), "fare_mean": round(float(d["base_passenger_fare"].mean()), 2),
            "pay_mean": round(float(d["driver_pay"].mean()), 2),
            "miles_p50": pct(d["trip_miles"], 50),
            "hours": hours,
        },
        "zone_hour_forecast": forecast,
    }


def write_demand(df, day, path):
    d = df[df["date"] == day].sort_values("request_datetime")
    midnight = pd.Timestamp(day)
    req = ((d["request_datetime"] - midnight).dt.total_seconds()).astype(int)
    wait = d["wait"].clip(lower=0, upper=3600).round().astype(int)
    lines = ["t,pu,do,wait,trip,miles,fare,pay"]
    for t, pu, do, w, tt, mi, fa, pa_ in zip(req, d["PULocationID"], d["DOLocationID"], wait, d["trip_time"], d["trip_miles"], d["base_passenger_fare"], d["driver_pay"]):
        lines.append(f"{t},{pu},{do},{w},{tt},{mi:.2f},{fa:.2f},{pa_:.2f}")
    write_gz(path, ("\n".join(lines) + "\n").encode())


def write_zones(zones, g, path):
    out = []
    present = set(g["zone"].tolist())
    for zid in sorted(zones):
        geom = zones[zid]["geom"].simplify(40.0)
        polys = list(geom.geoms) if geom.geom_type == "MultiPolygon" else [geom]
        rings = []
        for p in polys:
            xs, ys = p.exterior.coords.xy
            lon, lat = TO_LL.transform(np.asarray(xs), np.asarray(ys))
            rings.append([[round(float(a), 5), round(float(b), 5)] for a, b in zip(lon, lat)])
        rp = zones[zid]["geom"].representative_point()
        clon, clat = TO_LL.transform(rp.x, rp.y)
        out.append({
            "id": zid, "name": zones[zid]["name"], "lat": round(clat, 5), "lng": round(clon, 5),
            "served": zid in present, "rings": rings,
        })
    path.write_text(json.dumps(out, separators=(",", ":")), encoding="utf-8")


def poi_kind(tags):
    if tags.get("railway") == "station":
        return "station"
    for key in ("tourism", "amenity", "leisure", "shop"):
        v = tags.get(key)
        if v and v in POI_WEIGHT:
            return v
    if "historic" in tags:
        return "historic"
    if tags.get("building") in ("office", "commercial", "skyscraper"):
        return "office"
    if "building" in tags:
        return "building"
    return None


def write_places(raw, zones, g, path):
    data = json.loads((raw / "osm_places.json").read_text(encoding="utf-8"))["elements"]
    cand = []
    for e in data:
        tags = e.get("tags", {})
        name = ascii_name(tags.get("name", ""))
        kind = poi_kind(tags)
        if sum(ch.isalpha() for ch in name) < 2 or not kind:
            continue
        if "lat" in e:
            lat, lon = e["lat"], e["lon"]
        elif "center" in e:
            lat, lon = e["center"]["lat"], e["center"]["lon"]
        else:
            continue
        cand.append((name, kind, lat, lon, POI_WEIGHT[kind]))
    zid = assign_zones([c[2] for c in cand], [c[3] for c in cand], zones, 0.0)
    cand = [c for c, z in zip(cand, zid) if z > 0]

    names_at = defaultdict(dict)
    for e in g["edges"]:
        u, v, _, _, name, rank, _ = e
        if not name:
            continue
        for n in (u, v):
            if name not in names_at[n] or rank < names_at[n][name]:
                names_at[n][name] = rank
    seen = {}
    for n, nm in names_at.items():
        if len(nm) < 2:
            continue
        top = sorted(nm.items(), key=lambda kv: (kv[1], kv[0]))[:2]
        a, b = sorted(top, key=lambda kv: (kv[1], kv[0]))
        key = (a[0], b[0])
        if key not in seen:
            seen[key] = n
    for (a, b), n in seen.items():
        cand.append((f"{a} & {b}", "corner", float(g["lat"][n]), float(g["lon"][n]), 2))
    for zid_, z in zones.items():
        rp = z["geom"].representative_point()
        lon, lat = TO_LL.transform(rp.x, rp.y)
        cand.append((z["name"], "area", lat, lon, 3))

    cand.sort(key=lambda c: (-c[4], c[0], c[2], c[3]))
    kept = []
    by_name = defaultdict(list)
    for c in cand:
        key = c[0].lower()
        if any(haversine(c[2], c[3], k[2], k[3]) < 250 for k in by_name[key]):
            continue
        by_name[key].append(c)
        kept.append([c[0], c[1], round(c[2], 6), round(c[3], 6), c[4]])
    kept.sort(key=lambda c: (c[0].lower(), c[2], c[3]))
    write_gz(path, json.dumps(kept, separators=(",", ":"), ensure_ascii=False).encode("utf-8"))
    return len(kept)


def build(args):
    raw, out = Path(args.raw), Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    print("reading taxi zones")
    zones = load_zones(raw)
    print(f"  {len(zones)} Manhattan zones")
    print("building the road graph")
    g = build_graph(raw, zones)
    write_graph(g, out / "graph.bin.gz")
    write_zones(zones, g, out / "zones.json")
    print("reading Uber trips")
    df = load_trips(raw, args.month, zones)
    print(f"  {len(df)} Manhattan Uber trips in {args.month}")
    print("calibrating")
    cal = calibrate(df, g, args.day, args.seed)
    (out / "calibration.json").write_text(json.dumps(cal, indent=1) + "\n", encoding="utf-8")
    write_demand(df, args.day, out / "demand.csv.gz")
    print("building the place index")
    n = write_places(raw, zones, g, out / "places.json.gz")
    print(f"  {n} places")
    print(f"done, hour factors {cal['hour_factor']}")


def main():
    p = argparse.ArgumentParser(description="download and build the data files the server reads")
    sub = p.add_subparsers(dest="cmd", required=True)
    f = sub.add_parser("fetch")
    f.add_argument("--raw", default="data/raw")
    f.add_argument("--month", default="2026-08")
    b = sub.add_parser("build")
    b.add_argument("--raw", default="data/raw")
    b.add_argument("--out", default="data")
    b.add_argument("--month", default="2026-08")
    b.add_argument("--day", default="2026-08-07")
    b.add_argument("--seed", type=int, default=7)
    args = p.parse_args()
    fetch(args) if args.cmd == "fetch" else build(args)


if __name__ == "__main__":
    main()
