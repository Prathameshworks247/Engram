# Redis Streams — Interview Notes

A **stream** is an append-only log at a key. Each entry = **ID + field/value pairs**. Backed by a *radix tree* (`rax`) keyed by entry ID in real Redis. Think Kafka-topic-lite inside Redis.

## Entry IDs

Format `<millisecondsTime>-<sequenceNumber>`, both uint64. IDs are **monotonically increasing**; compare as `(ms, seq)` tuples.

| XADD id form | Behavior |
|--------------|----------|
| `1526919030474-0` | explicit, full |
| `1526919030474-*` | explicit ms, auto seq |
| `*` | auto ms (`time.Now().UnixMilli()`) + auto seq |

**Auto sequence rule:** if `ms == lastID.ms` → `seq = lastID.seq + 1`; else `seq = 0`; **except** when `ms == 0` and stream empty → `seq = 1` (because `0-0` is illegal).

**Validation errors (exact strings):**
- `0-0` → `ERR The ID specified in XADD must be greater than 0-0`
- `id <= lastID` → `ERR The ID specified in XADD is equal or smaller than the target stream top item`

XADD returns the resulting ID as a bulk string. TYPE of a stream key → `stream`.

## XRANGE key start end  (both inclusive)

- `-` = smallest possible (`0-0`), `+` = largest possible (`FFFF...-FFFF...`).
- Bare `ms` with no `-seq`: **start** defaults seq to `0`, **end** defaults seq to `MAX`.
- Reply shape: array of `[ id, [f1, v1, f2, v2, ...] ]`.

```
*1\r\n *2\r\n $3\r\n0-1\r\n *2\r\n $11\r\ntemperature\r\n $2\r\n36\r\n
```

## XREAD  — read entries *after* an ID (exclusive)

`XREAD [COUNT n] [BLOCK ms] STREAMS key [key...] id [id...]`

- N keys followed by N ids (unbalanced → error).
- Returns entries with `id > given` (strictly greater — exclusive lower bound), vs XRANGE which is inclusive.
- Reply shape: array of `[ streamKey, [ [id,[fields]], ... ] ]`, one per stream **that has new entries** (streams with none are omitted).
- Non-blocking with nothing new → **null array** `*-1\r\n`.

### Blocking (`BLOCK ms`)
- `ms > 0` → wait up to that many ms, then null array on timeout.
- `BLOCK 0` → block forever until an entry arrives.
- Implementation: snapshot the "after" IDs, then poll (sleep ~5ms) re-checking `entriesAfter`, or use a cond var broadcast on XADD.

### `$` as the id
- Only valid in XREAD. Resolves to **the stream's current `lastID` at the moment the command is received**. So the client only sees entries added *after* it started blocking. Must resolve `$` once, before entering the wait loop.

## Interview points
- **XRANGE inclusive vs XREAD exclusive** — classic gotcha.
- IDs are `(time, seq)` so multiple entries in the same millisecond stay ordered.
- `$` = "tail -f from now"; a concrete last ID = "give me the backlog since X".
- Consumer groups (`XGROUP`, `XREADGROUP`, `XACK`, PEL) are the real power feature — not in this challenge but worth naming: at-least-once delivery, per-consumer pending lists, `XCLAIM` for reassigning stuck messages.
- Streams don't drop data on read (unlike lists/pubsub); trim with `XADD ... MAXLEN` / `XTRIM`.
