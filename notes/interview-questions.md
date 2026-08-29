# Redis Interview Questions — Groups 1–6

30 questions with answers, covering Fundamentals, Lists, Streams, Transactions,
Optimistic Locking, and Replication. Companion to `notes/01`…`06`.

---

## A. Fundamentals & protocol

### 1. Why is Redis fast even though command execution is single-threaded?
Data lives in RAM, so no disk seeks. One thread owns all data → no locks, no contention, no context-switch overhead. Data structures are O(1)/O(log n). I/O is multiplexed with epoll/kqueue so one thread services thousands of sockets. The RESP wire protocol is tiny and length-prefixed, so parsing is cheap. (Redis 6+ can use extra threads for socket read/write only; the command still runs on one thread.)

### 2. What is RESP and how does message framing work?
REdis Serialization Protocol. Types are tagged by a one-byte prefix: `+` simple string, `-` error, `:` integer, `$` bulk string, `*` array. Aggregates are **length-prefixed** — `*3` = three elements follow, `$4` = next four bytes are the value. You never scan for a delimiter; you read exactly the announced byte count, which makes values binary-safe (they can contain `\r\n`).

### 3. A client sends two commands back-to-back on one connection. How does the server not get confused?
TCP is a byte stream with no message boundaries, but RESP length prefixes tell the parser exactly where command 1 ends. The connection handler is a loop: parse one complete command, execute, write reply, repeat. A buffered reader may already hold command 2's bytes; that's fine.

### 4. `+OK` vs `$2\r\nOK\r\n` — when is each used?
`+OK` is a *simple string*: short, newline-free status responses (`SET`, `PING`). `$2\r\nOK\r\n` is a *bulk string*: length-prefixed and binary-safe, used for actual data values (`GET`, `LPOP`). A missing key is the *null bulk string* `$-1\r\n`.

### 5. How does key expiry actually work?
The TTL is stored as an absolute timestamp on the entry. **Lazy expiration:** on every access, if `now > expireAt`, delete and report missing. **Active expiration:** a background job samples ~20 random keys with TTLs several times a second and removes expired ones, repeating while the expired fraction stays high — so keys that are never read still get cleaned up.

### 6. `SET key val EX 10` vs `EXPIRE key 10` — difference?
`SET ... EX` sets value and TTL atomically in one command. `EXPIRE` adds a TTL to an already-existing key (returns 0 if the key is absent). `SET` also has `PX` (ms), `EXAT`/`PXAT` (absolute), `NX`/`XX`, and `KEEPTTL`.

### 7. How would you handle 10k concurrent client connections in your implementation?
Goroutine per connection (Go): `Accept()` in a loop, `go handleConn(conn)`. Shared state guarded by a mutex. Real Redis instead uses a single-threaded event loop over non-blocking sockets — same external behavior, avoids the goroutine/lock overhead.

---

## B. Lists

### 8. `LPUSH mylist a b c` — final order, and why?
`c b a`. Each argument is pushed to the head one at a time, so the last one ends up first. `RPUSH` preserves argument order.

### 9. Is `LRANGE key 0 -1` inclusive? What does `-1` mean?
Both bounds are inclusive; `-1` is the last element, `-2` the second-last. `LRANGE key 0 -1` returns the whole list. Out-of-range bounds are clamped; an empty/missing list returns an empty array (not null).

### 10. How is a Redis list stored — is it a real linked list?
Logically a doubly-linked list; physically a **quicklist**: a linked list whose nodes are `listpack`s (compact byte-array blocks of ~dozens of elements). O(1) at both ends, but far less memory overhead than one node + two pointers per element.

### 11. How does `BLPOP` block without freezing the single-threaded server?
It doesn't sleep. The client is *parked*: registered in a per-key wait list; control returns to the event loop. When a `PUSH` makes the key non-empty, the server serves parked clients in FIFO order right after the current command finishes. A from-scratch version can poll with a short sleep.

### 12. `BLPOP a b c 0` — semantics?
Check keys left to right, pop from the first non-empty one. `0` = block indefinitely; a positive number is a timeout in seconds (fractional allowed), after which it returns a null array.

### 13. What happens to a list key when its last element is popped?
The key is deleted. Redis keeps no empty aggregate keys (list/set/hash/zset). `EXISTS` returns 0 afterward.

### 14. When would you use a Redis list vs a stream?
List: simple job queue (`LPUSH` + `BRPOP`), capped feed (`LPUSH` + `LTRIM`), stack. It's one-consumer-per-message with no replay. Stream: persisted log, many independent readers, replay from any ID, consumer groups for at-least-once work sharing.

---

## C. Streams

### 15. Structure of a stream entry ID, and why two parts?
`<millisecondsTime>-<sequenceNumber>`, both 64-bit. The time part orders entries chronologically; the sequence disambiguates multiple entries within the same millisecond. IDs must be strictly increasing per stream.

### 16. `XADD` ID forms?
`5-3` explicit; `5-*` explicit time, auto sequence; `*` auto time (now) + auto sequence. Auto-sequence rule: if the time part equals the last entry's time, `seq = lastSeq + 1`, else `seq = 0` — except time `0` on an empty stream starts at `0-1` because `0-0` is illegal.

### 17. `XRANGE` vs `XREAD`?
`XRANGE key start end` — range scan, **both bounds inclusive**, ascending; `-`/`+` mean min/max. `XREAD STREAMS key id` — returns entries **strictly greater** than `id` (exclusive); it's the primitive for tailing and blocking reads, and can read several streams at once.

### 18. What does `$` mean in `XREAD`?
"From now on." At the instant the command is received it resolves to the stream's current last ID, so the caller sees only entries added *after* it started listening (like `tail -f`). It must be resolved once, before entering the blocking wait.

### 19. How do you implement `XREAD BLOCK 0`?
Snapshot the per-stream "after" IDs. Then loop: check for entries greater than the snapshot; if none, wait — either poll with a small sleep or block on a condition variable that `XADD` broadcasts. `BLOCK 0` = no deadline; `BLOCK <ms>` returns a null array when the deadline passes.

### 20. Streams vs Pub/Sub vs Lists for messaging?
Pub/Sub: fire-and-forget, no persistence, offline subscribers miss messages. List: a queue, single consumer per message, no replay. Stream: persisted, replayable log with consumer groups (`XREADGROUP`/`XACK`), per-consumer pending lists, and `XCLAIM` to reassign stuck messages — closest to Kafka.

### 21. Do stream entries get consumed/removed on read?
No. Reading never deletes. Cap growth explicitly with `XADD ... MAXLEN ~ N` or `XTRIM`. Consumer groups track *delivery state* (the PEL) separately; entries persist until trimmed.

---

## D. Transactions (MULTI/EXEC)

### 22. Does Redis roll back a transaction if a command fails?
No. There is no rollback. A queued command that errors at **runtime** (e.g. `INCR` on a string) puts its error in the results array while the other queued commands still execute. Only a command that's malformed at **queue time** (unknown command, bad arity) marks the transaction dirty so `EXEC` aborts everything with `EXECABORT`.

### 23. How is a transaction atomic with no locks?
Single-threaded execution. Once `EXEC` starts, all queued commands run consecutively in the event loop with no other client's command interleaved.

### 24. What do `MULTI`, queued commands, and `EXEC` return?
`MULTI` → `+OK`. Each subsequent command → `+QUEUED`. `EXEC` → an array with one element per queued command, each being that command's normal reply. `MULTI` then `EXEC` with nothing queued → empty array `*0\r\n`.

### 25. `EXEC`/`DISCARD` without `MULTI`?
Errors: `-ERR EXEC without MULTI`, `-ERR DISCARD without MULTI`. `DISCARD` inside a transaction throws away the queue and returns `+OK`. Transaction state is per connection.

### 26. Transaction vs Lua script vs Functions?
All run atomically. A transaction is a fixed, pre-declared command list with no branching and no visibility of intermediate results. Lua scripts / Functions allow logic, loops, and reading a value mid-script to decide the next write.

---

## E. Optimistic locking (WATCH)

### 27. What does `WATCH` lock?
Nothing. It registers interest in one or more keys. The only effect: if any watched key is modified (by any client) before this connection's `EXEC`, that `EXEC` runs no commands and returns a null array (`*-1\r\n`). The client then retries its whole read-modify-write.

### 28. Optimistic vs pessimistic locking?
Pessimistic: take a lock before touching data; others block (e.g. `SELECT ... FOR UPDATE`). Optimistic: don't lock; do the work; at commit, check whether the data changed and retry if so. Optimistic wins when conflicts are rare (no blocking, no deadlocks); pessimistic wins under heavy contention.

### 29. Implement a safe compare-and-set counter with `WATCH`.
```
WATCH k
v = GET k
MULTI
SET k (v + 1)
EXEC            # nil  -> someone changed k, loop and retry
                # [+OK] -> committed
```
`WATCH` must come before `MULTI` (watching inside a transaction is an error). `EXEC`, `DISCARD`, and `UNWATCH` all clear the watch set; so does a dropped connection.

### 30. How would you implement `WATCH` from scratch?
Give the store a per-key mutation counter, bumped by every write (`SET`, `INCR`, `RPUSH`, `XADD`, …). On `WATCH k`, record the current counter for `k` on the client. On `EXEC`, compare each watched key's counter to the recorded value; if any differs, skip execution and return nil. Watching a missing key works automatically: its counter is 0, and creating it bumps it.

---

## F. Replication

### 31. Walk through what happens when a replica connects to a master.
TCP connect → handshake: replica sends `PING`, `REPLCONF listening-port <p>`, `REPLCONF capa psync2`, `PSYNC ? -1`. Master replies `+FULLRESYNC <replid> 0` then `$<len>\r\n<RDB bytes>`. Replica loads the RDB, then the master streams every subsequent write command on the same socket. The replica applies them and replies to nothing except `REPLCONF GETACK` (with `REPLCONF ACK <offset>`).

### 32. Is Redis replication synchronous or asynchronous?
Asynchronous by default — the master replies to the client before replicas confirm, so a failover can lose the last few writes. `WAIT <numreplicas> <ms>` lets a client block until N replicas acknowledge (or timeout), trading latency for durability, but the write is already committed on the master regardless.

### 33. What are the replication ID and replication offset for?
The **replid** identifies the master's dataset lineage. The **offset** is a byte cursor into the replication stream. A reconnecting replica sends `PSYNC <replid> <offset>` to request a **partial resync** — the master replays just the missing bytes from an in-memory backlog buffer instead of shipping a whole new RDB.

### 34. Full resync vs partial resync?
Full: master serializes the entire dataset to RDB and sends it (first sync, or when the backlog can't cover the gap). Partial: master streams only the bytes the replica missed from a circular backlog buffer — cheap recovery after a brief disconnect.

### 35. Why must a replica NOT reply to propagated commands?
The replication socket carries data one way; the only thing the replica writes back is `REPLCONF ACK`. If it echoed `+OK`/`+PONG` for propagated `SET`/`PING`, those bytes would land in the master's stream and corrupt its parsing of ACKs — a classic implementation bug.

### 36. How does the replication offset / `REPLCONF ACK` work?
The replica adds the RESP byte length of every command it receives from the master to a running `offset`. On `REPLCONF GETACK *` it replies `REPLCONF ACK <offset>`, where the offset counts commands processed **before** this GETACK (then it adds the GETACK's own bytes). The master uses these ACKs to know how far each replica has caught up.

### 37. How does `WAIT numreplicas timeout` work internally?
The master records its current offset, sends `REPLCONF GETACK *` to every replica, and counts replicas whose returned ACK offset ≥ that recorded offset. It returns that count as soon as it reaches `numreplicas`, or when the timeout elapses. If no writes have been propagated yet, it returns the connected-replica count immediately.

### 38. What happens on master failure, and how is it handled?
Un-acked writes on the dead master are lost (the async tradeoff). **Redis Sentinel** monitors instances, agrees via quorum that the master is down, promotes a replica, reconfigures the other replicas to follow it, and tells clients the new address. **Redis Cluster** does similar failover per shard.

### 39. How do you scale reads vs writes in Redis?
Reads: send them to replicas (`READONLY` on Cluster, or just connect to replica nodes) — replication fans the data out. Writes: still funnel through a single master per dataset. To scale writes you need **Redis Cluster**, which hash-slots keys across multiple master shards.

### 40. Why does the master propagate `SET` to replicas but not `GET` or a client's `PING`?
Only commands that mutate state need to be replayed on replicas to keep them in sync. Read-only commands (`GET`) and client `PING`s change nothing, so forwarding them would just waste bandwidth and inflate the offset. (The master *may* still send its own periodic `PING` to replicas as a keepalive — that one is counted in the offset.)

---

## G. Cross-cutting / system design

### 41. When is Redis the wrong choice?
When the working set doesn't fit in RAM (RAM is the hard ceiling); when you need rich queries/joins/secondary indexes (use a real DB); when you can't tolerate losing the last few seconds of writes on a crash and can't afford `appendfsync always`; when you need strong multi-key transactions across shards.

### 42. RDB vs AOF persistence (one-liner each)?
RDB: point-in-time binary snapshot, compact, fast restart, but you lose everything since the last snapshot. AOF: append every write command to a log, replay on restart, far less data loss (configurable fsync), but larger files and slower restart. Common setup: run both.

### 43. How does Redis evict data when memory is full?
Per `maxmemory-policy`: `noeviction` (reject writes), `allkeys-lru`/`allkeys-lfu`/`allkeys-random` (evict any key), or `volatile-*` (only keys with a TTL). LRU/LFU are approximated by sampling, not exact.
