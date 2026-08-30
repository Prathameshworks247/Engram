# Sorted Sets — Interview Notes

## What the CodeCrafters stages asked you to build

1. **Create a sorted set** — `ZADD key score member` on a missing key creates it; returns count of **new** members.
2. **Add members** — `ZADD` on an existing set; updating an existing member's score returns `0` (not new).
3. **Retrieve member rank** — `ZRANK key member` → 0‑based index in sorted order, or null if key/member missing.
4. **List sorted set members** — `ZRANGE key start stop` (inclusive) → members in score order.
5. **ZRANGE with negative indexes** — `-1` = last, `-2` = second‑last, etc.
6. **Count sorted set members** — `ZCARD key` → cardinality (0 if missing).
7. **Retrieve member score** — `ZSCORE key member` → score as a bulk string, or null if missing.
8. **Remove a member** — `ZREM key member [member...]` → count removed; the key is deleted when it becomes empty.

## Data model

A sorted set = unique **members** (strings), each with a **float64 score**. Iteration order is **(score ascending, then member lexicographically for ties)**. That ordering is the whole point — leaderboards, priority queues, time‑series buckets, rate limiters (`ZADD` timestamps + `ZREMRANGEBYSCORE`).

### How Redis stores it (two structures kept in sync)
- A **hash map** `member -> score` for O(1) `ZSCORE` / membership / `ZADD` updates.
- A **skip list** ordered by `(score, member)` for O(log N) rank, range, and insert/delete.

A skip list is a linked list with multiple "express lane" levels chosen randomly per node; it gives balanced‑tree performance with far simpler code and good cache behaviour. Redis also keeps span counts on forward pointers so `ZRANK` is O(log N) rather than O(N).

This challenge's implementation just uses the map plus an on‑demand sort — fine for small sets, O(N log N) per query.

### Score formatting
`ZSCORE` returns the score as a string with no trailing zeros: `10` not `10.0`, `8.2` stays `8.2`, `3.14` stays. In Go: `strconv.FormatFloat(f, 'f', -1, 64)`.

## Command cheat‑sheet (beyond the challenge)

| Command | Meaning |
|---------|---------|
| `ZADD k [NX\|XX] [GT\|LT] [CH] [INCR] s m` | add/update; flags gate the write, `CH` counts changed, `INCR` acts like `ZINCRBY` |
| `ZINCRBY k delta m` | add `delta` to a member's score |
| `ZRANGE k a b [REV] [BYSCORE\|BYLEX] [LIMIT off cnt] [WITHSCORES]` | the swiss‑army range command |
| `ZRANGEBYSCORE`, `ZRANGEBYLEX` | legacy range variants |
| `ZRANK` / `ZREVRANK` | 0‑based position from front / back |
| `ZPOPMIN` / `ZPOPMAX` / `BZPOPMIN` | pop lowest/highest, optionally blocking → priority queue |
| `ZREMRANGEBYRANK\|BYSCORE\|BYLEX` | bulk delete |
| `ZUNIONSTORE` / `ZINTERSTORE` / `ZDIFFSTORE` | set algebra with score aggregation |

## Probable interview questions

**Q: How does Redis keep a sorted set both "a set" and "sorted"?**
Two structures: a dict `member → score` for O(1) point lookups and de‑dup, and a skip list keyed by `(score, member)` for O(log N) ordered operations. Every write updates both.

**Q: Why a skip list instead of a red‑black tree?**
Simpler to implement and to make lock‑free/concurrent‑friendly, similar asymptotic performance, and it naturally supports fast range scans (just walk the bottom list from the found node). Redis augments it with span counts so rank queries are also O(log N).

**Q: Ties — two members with the same score?**
Ordered lexicographically by the member string. That also makes `ZRANGEBYLEX` meaningful: if *all* scores are equal, the set is a sorted collection of strings.

**Q: Build a leaderboard "top 10 and my rank."**
`ZREVRANGE board 0 9 WITHSCORES` for the top 10; `ZREVRANK board <me>` for my position; `ZSCORE board <me>` for my points. All O(log N) or better.

**Q: Use a sorted set as a sliding‑window rate limiter.**
For each request: `ZADD key now now`, `ZREMRANGEBYSCORE key 0 (now - window)`, `ZCARD key`; allow if the count ≤ limit. Set a TTL on the key. (Members must be unique — use `now` plus a counter/uuid.)

**Q: `ZADD` return value nuances?**
By default it returns the number of **newly added** members (score updates on existing members don't count). With `CH` it returns the number **changed** (added *or* score‑updated). With `INCR` it behaves like `ZINCRBY` and returns the new score.

**Q: Sorted set vs a plain set vs a hash?**
Set: unordered unique strings, membership tests, set algebra. Hash: field→value map under one key. Sorted set: unique strings **ranked by a number**, with range/rank queries — the only one of the three that answers "give me items 10–20 by score."
