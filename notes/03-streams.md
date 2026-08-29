# Redis Streams — Interview Notes

## What the CodeCrafters stages asked you to build

1. **The TYPE command** — `TYPE key` → `string` / `list` / `stream` / `none`.
2. **Create a stream** — `XADD key 0-1 f v` creates the stream, returns the ID; `TYPE` → `stream`.
3. **Validating entry IDs** — reject `0-0`; reject an ID `<=` the last one (exact error strings).
4. **Partially auto-generated IDs** — `XADD key 5-* ...` auto-picks the sequence number.
5. **Fully auto-generated IDs** — `XADD key * ...` auto-picks `ms` (now) and sequence.
6. **Query entries from stream** — `XRANGE key start end` (both inclusive).
7. **Query with `-`** — `-` = smallest possible ID.
8. **Query with `+`** — `+` = largest possible ID.
9. **Query single stream using XREAD** — `XREAD STREAMS key id` → entries **after** `id`.
10. **Query multiple streams using XREAD** — `XREAD STREAMS k1 k2 id1 id2`.
11. **Blocking reads** — `XREAD BLOCK <ms> STREAMS key id`, null array on timeout.
12. **Blocking reads without timeout** — `BLOCK 0` = wait forever.
13. **Blocking reads using `$`** — `$` means "only entries added after this call" (resolve to current last ID).

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

## Probable interview questions

**Q: Difference between `XRANGE` and `XREAD`?**
`XRANGE key start end` is a range scan, **both bounds inclusive**, ascending. `XREAD STREAMS key id` returns entries **strictly greater** than `id` (exclusive) and is the building block for tailing / blocking reads.

**Q: What does the entry ID `1526919030474-0` mean, and why two parts?**
`<millisecondsTime>-<sequence>`. The millisecond part orders entries in time; the sequence disambiguates multiple entries added within the same millisecond. IDs must strictly increase.

**Q: What is `$` in `XREAD`?**
"From now on." It resolves, at the moment the command is received, to the stream's current last ID, so the caller only sees entries added *after* it started listening. Like `tail -f`. A concrete ID instead means "give me everything since this point" (the backlog).

**Q: How do you implement a blocking `XREAD BLOCK 0`?**
Snapshot the target "after" IDs, then either (a) poll: sleep a few ms, re-check for entries greater than the snapshot; or (b) use a condition variable that `XADD` broadcasts. `BLOCK 0` = no deadline; `BLOCK ms` = return a null array when the deadline passes.

**Q: Streams vs Pub/Sub vs Lists for messaging?**
Pub/Sub = fire-and-forget, no history, offline subscribers miss messages. Lists = a queue, but one consumer per message and no replay. Streams = persisted log with IDs, multiple independent readers, replay from any point, and **consumer groups** (`XREADGROUP`/`XACK`) for at-least-once work distribution with per-consumer pending lists and `XCLAIM` to recover stuck messages.

**Q: Do stream entries disappear when read?**
No — reading doesn't consume. You cap growth explicitly with `XADD ... MAXLEN ~ N` or `XTRIM`. (Consumer groups track *delivery* via the PEL, but the entries themselves stay until trimmed.)
