# Geospatial Commands — Interview Notes

## What the CodeCrafters stages asked you to build

1. **Respond to GEOADD** — `GEOADD key lon lat member` → integer (members added).
2. **Validate coordinates** — reject lon outside `[-180, 180]` or lat outside `[-85.05112878, 85.05112878]` with `ERR invalid longitude,latitude pair <lon>,<lat>`.
3. **Store a location** — actually add the member to a sorted set (score computed from the coordinates).
4. **Calculate location score** — the score must equal Redis' 52‑bit interleaved geohash for that lon/lat, exactly (`GEOADD ... ` ≡ `ZADD ... <that number> ...`).
5. **Respond to GEOPOS** — `GEOPOS key member [member...]` → array of `[lon, lat]` (or null per missing member).
6. **Decode coordinates** — recover lon/lat from the stored score (cell centre).
7. **Calculate distance** — `GEODIST key m1 m2 [unit]` → haversine distance, default metres, formatted `%.4f`.
8. **Search within radius** — `GEOSEARCH key FROMLONLAT lon lat BYRADIUS r <unit>` → members within the circle, nearest first.

## Core idea: geo data *is* a sorted set

Redis has **no separate geo type**. `GEOADD` just does `ZADD` where the score is a **geohash** — a single integer that encodes both coordinates such that numeric ordering ≈ spatial proximity along a space‑filling (Z‑order / Morton) curve. So `ZRANGE`, `ZREM`, `ZSCORE`, `ZCARD` all work on a geo key.

### Encoding (must match Redis bit‑for‑bit)
```
lat range  = [-85.05112878, 85.05112878]   (Web-Mercator clamp; keeps the map square)
lon range  = [-180, 180]
step       = 26 bits per axis  ->  52-bit hash

latOffset = (lat - latMin) / (latMax - latMin) * 2^26   -> uint32
lonOffset = (lon - lonMin) / (lonMax - lonMin) * 2^26   -> uint32
score     = interleave64(latOffset, lonOffset)          -> lat bits in even positions, lon bits in odd
```
`interleave64` spreads each 26‑bit value across 52 bits using the classic magic‑constant bit‑twiddling (`x = (x | x<<8) & 0x00FF00FF...` cascade). Decoding = `deinterleave64` → integer cell indices → convert back to the min/max of that cell → return the **centre**. Decoding is lossy (you get the cell centre, ~0.6 m precision at step 26), so `GEOPOS` after `GEOADD` returns *nearly* the input, not exactly.

### Distance — haversine
```
a = sin²(Δφ/2) + cos φ1 · cos φ2 · sin²(Δλ/2)
d = 2 · R · asin(√a),   R = 6372797.560856 m   (Redis' EARTH_RADIUS_IN_METERS)
```
Redis decodes both members to lon/lat then applies this. Reply is a bulk string `%.4f` in the requested unit (m / km / mi / ft).

### `GEOSEARCH` (and legacy `GEORADIUS`)
Naive: decode every member, haversine to the query point, keep those `≤ radius`. Redis is smarter — it computes the geohash cell(s) covering the search area (the centre cell plus its 8 neighbours, at a step chosen from the radius) and only scans members whose hash falls in those ranges via `ZRANGEBYSCORE`, then does the exact haversine filter. That turns an O(N) scan into O(log N + hits).

## Probable interview questions

**Q: Does Redis have a dedicated geospatial data structure?**
No. `GEO*` commands are a thin layer over sorted sets; the score is a 52‑bit geohash of the coordinates. That's why geo keys respond to `ZCARD`, `ZSCORE`, `ZREM`, etc.

**Q: What is a geohash and why does it help?**
It interleaves the bits of latitude and longitude into one number that follows a Z‑order (Morton) space‑filling curve, so points that are numerically close are usually geographically close. That lets a 1‑D index (the sorted set) answer 2‑D proximity queries with range scans.

**Q: Why is the latitude range ±85.05° instead of ±90°?**
The Web‑Mercator projection (used to keep the encoded space a square grid) goes to infinity at the poles; ±85.05112878° is where it becomes square. Points beyond that can't be encoded.

**Q: Why doesn't `GEOPOS` return exactly what I put in with `GEOADD`?**
Encoding quantises to a 2^26 × 2^26 grid and decoding returns the **centre** of the cell your point landed in. The error is sub‑metre but non‑zero.

**Q: How does `GEOSEARCH ... BYRADIUS` avoid scanning every member?**
It derives the geohash prefix(es) covering the query circle — the centre cell and its 8 neighbours at an appropriate precision — and uses `ZRANGEBYSCORE` on each contiguous hash range, then applies the exact haversine check to discard corner false‑positives.

**Q: `GEODIST` units and formatting?**
Default metres; `km`, `mi`, `ft` supported. Result is a bulk string with 4 decimal places. It's computed by decoding both members and taking the haversine great‑circle distance with `R = 6372797.560856 m`.

**Q: Alternatives for real geo workloads?**
For heavy spatial querying you'd reach for PostGIS (R‑tree / GiST indexes, polygons, projections) or a dedicated engine. Redis GEO is great for "nearby X" lookups on a moving set of points with the rest of your data already in Redis.
