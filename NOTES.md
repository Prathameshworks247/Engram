# Build Your Own Redis — Learnings Log

Quick revision digest. One section per CodeCrafters group, appended as each group is
completed and submitted. Deep dives + interview Q&A live in `notes/`.

**Workflow:** finish a group → all its stages green on CodeCrafters → append its section
here → stop for review before starting the next group.

| # | Group | Status | Detail file |
|---|-------|--------|-------------|
| 1 | Fundamentals | ✅ done | [notes/01-fundamentals.md](notes/01-fundamentals.md) |
| 2 | Lists | ✅ done | [notes/02-lists.md](notes/02-lists.md) |
| 3 | Streams | ✅ done | [notes/03-streams.md](notes/03-streams.md) |
| 4 | Transactions | ✅ done | [notes/04-transactions.md](notes/04-transactions.md) |
| 5 | Optimistic Locking | ✅ done | [notes/05-optimistic-locking.md](notes/05-optimistic-locking.md) |
| 6 | Replication | ✅ done | [notes/06-replication.md](notes/06-replication.md) |
| 7 | RDB Persistence | ✅ done | [notes/07-rdb-persistence.md](notes/07-rdb-persistence.md) |
| 8 | AOF Persistence | 🔶 5/10 | [notes/08-aof-persistence.md](notes/08-aof-persistence.md) |
| 9 | Pub/Sub | ⬜ todo | — |
| 10 | Sorted Sets | ⬜ todo | — |
| 11 | Geospatial | ⬜ todo | — |
| 12 | Authentication | ⬜ todo | — |

General Q&A across groups 1–6: [notes/interview-questions.md](notes/interview-questions.md).

---

## 1. Fundamentals

**Built:** TCP listener on 6379, per-connection read loop, `PING` / `ECHO` / `SET` /
`GET` / `TYPE`, key expiry (`PX` / `EX`).

**Key learnings**
- RESP is **length-prefixed**: `*N` = array of N, `$K` = next K bytes. Never scan for a
  delimiter → binary-safe. Read exactly the announced byte count.
- One connection = one loop: parse a full command, reply, repeat. TCP has no message
  boundaries; the length prefixes are the framing.
- Reply types: `+simple`, `-error`, `:int`, `$bulk`, `*array`, null bulk `$-1\r\n`.
  `SET`→`+OK` (simple), `GET`→bulk or `$-1`.
- Expiry = absolute timestamp on the entry; **lazy** delete on access (`now > expireAt`)
  + Redis also does active random sampling.
- Concurrency: goroutine-per-conn in Go; real Redis is a single-threaded event loop.
  Shared map needs a mutex; blocking cmds later need a `sync.Cond` on that mutex.

**Gotchas:** `SET k v PX 100` — TTL is a duration on write; store it as `time.Now().Add(...)`.

---

## 2. Lists

**Built:** `RPUSH` / `LPUSH` / `LRANGE` / `LLEN` / `LPOP` (with count) / `BLPOP`.

**Key learnings**
- `LPUSH k a b c` → list is `c b a` (each arg pushed to head). `RPUSH` keeps order.
- `LRANGE` bounds are **both inclusive**; negative indexes count from the end (`-1` =
  last). Missing/empty list → empty array `*0`, not null.
- `LPOP` no count → bulk or `$-1`; with count → array. When a list empties, **delete the
  key** (Redis keeps no empty aggregates).
- Storage is a *quicklist* (linked list of `listpack` nodes): O(1) ends, low overhead.
- `BLPOP a b c 0`: check keys left→right, pop from first non-empty; `0` = block forever.
  Implemented by polling with a short sleep (real Redis parks the client FIFO per key).

**Gotchas:** needed a typed store — moved from `map[string]string` to
`map[string]entry{value any}` here so lists/streams/etc. can coexist.

---

## 3. Streams

**Built:** `TYPE` returns `stream`; `XADD` (explicit / `ms-*` / `*` IDs, validation);
`XRANGE` (`-` / `+`); `XREAD` single + multi + `BLOCK` + `$`.

**Key learnings**
- Entry ID = `<ms>-<seq>`, strictly increasing. Compare as `(ms, seq)` tuples.
- Auto-seq rule: same ms as last → `lastSeq+1`; else `0`; special: ms `0` on empty stream
  → `0-1` (because `0-0` is illegal).
- Exact error strings matter: `ERR The ID specified in XADD must be greater than 0-0`
  and `... is equal or smaller than the target stream top item`.
- **`XRANGE` inclusive, `XREAD` exclusive** (strictly greater than the given id).
- `$` in `XREAD` = resolve once to the stream's current `lastID` → only see new entries.
- Reply shapes: XRANGE → `[[id,[f,v,...]], ...]`; XREAD → `[[key,[entries]], ...]`,
  streams with nothing new omitted; non-blocking with nothing → null array `*-1`.

**Gotchas:** don't create the stream key on a failed `XADD` validation — build a temp
`*Stream`, only insert into the store on success.

---

## 4. Transactions

**Built:** `INCR`; `MULTI` / `EXEC` / `DISCARD`; `+QUEUED`; error-in-results;
per-connection tx state.

**Key learnings**
- **No rollback.** A queued command failing at runtime puts its error in the results
  array; siblings still run. Only a queue-time malformed command aborts everything
  (`EXECABORT`) — not tested here.
- Atomicity comes from single-threaded execution, not locks: once `EXEC` starts, all
  queued commands run back-to-back.
- `MULTI`→`+OK`, each queued cmd→`+QUEUED`, `EXEC`→array of each cmd's normal reply;
  empty tx → `*0`.
- `EXEC` / `DISCARD` without `MULTI` → errors. Tx state is per `*Client`.
- Neat trick: each handler already returns a full RESP string, so "array of replies" =
  `*N\r\n` + concatenation.

**Gotchas:** had to refactor the stateless `dispatch` into `(*Client).dispatch` +
`execCommand` so per-connection tx state has somewhere to live.

---

## 5. Optimistic Locking

**Built:** `WATCH` / `UNWATCH`; abort `EXEC` (→ null array) if a watched key changed;
watch missing keys; unwatch on `EXEC` / `DISCARD`.

**Key learnings**
- `WATCH` locks **nothing** — it just registers interest. Effect: next `EXEC` on this
  conn returns `*-1` and runs nothing if any watched key was modified meanwhile.
- Implemented with a per-key **mutation counter** in the store, bumped by every write
  (`SET`/`INCR`/`RPUSH`/`LPOP`/`BLPOP`/`XADD`). `WATCH` snapshots the counter;
  `EXEC` compares.
- Watching a missing key works for free: counter is 0, creating it bumps to 1 → mismatch.
- `WATCH` inside `MULTI` → `ERR WATCH inside MULTI is not allowed`.
- `EXEC`, `DISCARD`, `UNWATCH`, and a dropped connection all clear the watch set.
- This is CAS / optimistic concurrency: assume no conflict, detect at commit, client
  retries the whole read-modify-write.

**Gotchas:** every mutation path needs the `store.touch(key)` call — easy to miss one
(BLPOP pop, LPOP-to-empty, XADD-creating-stream).

---

## 6. Replication

**Built:** `--port`, `--replicaof`; `INFO replication`; 4-step handshake both sides;
`+FULLRESYNC` + empty-RDB transfer; write propagation to N replicas; `REPLCONF ACK`
offset tracking; `WAIT`.

**Key learnings**
- Handshake: replica → `PING`, `REPLCONF listening-port`, `REPLCONF capa psync2`,
  `PSYNC ? -1`; master → `+FULLRESYNC <replid> 0` then `$<len>\r\n<RDB bytes>`
  (no trailing CRLF), then a live command stream.
- `replid` = 40 hex chars (data lineage); `master_repl_offset` = byte cursor into the
  stream. `PSYNC <replid> <offset>` enables partial resync from a backlog buffer.
- Replication is **async** — master replies before replicas confirm; failover can lose
  the tail. `WAIT n ms` blocks for n acks or timeout (tunable durability, not sync).
- Replica must send **no reply** to propagated commands except `REPLCONF ACK` — echoing
  `+OK`/`+PONG` corrupts the master's ACK parsing.
- **Offset accounting (the fiddly bit):** replica adds RESP byte length of every command
  from master. On `REPLCONF GETACK *`, reply `REPLCONF ACK <offset>` where offset counts
  commands **before** this GETACK, *then* add the GETACK's own bytes. (Stage "ACKs with
  no commands" wants a hardcoded `0`; "ACKs with commands" wants the running total.)
- `WAIT`: snapshot master offset → send `GETACK` to all replicas → count replicas whose
  ack ≥ snapshot → return on count reached or timeout. If nothing propagated yet, return
  connected-replica count immediately.
- Only propagate mutating commands; `GET` and client `PING` are not forwarded.

**Bug hit & fixed:** replica was counting the GETACK command's own bytes into the offset
before replying → returned `37` where the tester wanted `0`. Fix: reply with the current
offset first, then increment.

---

## 7. RDB Persistence

**Built:** `--dir` / `--dbfilename`; `CONFIG GET`; load the RDB at startup;
`KEYS *`; `GET` served from the file; honour per-key expiries.

**Key learnings**
- RDB = point-in-time binary snapshot. Layout: `REDIS` + 4-digit version → opcode stream
  → `0xFF` EOF → 8-byte CRC64.
- Opcodes: `0xFA` aux (k,v), `0xFE` selectdb, `0xFB` resizedb (2 lengths),
  `0xFD` expire-seconds (4-byte LE), `0xFC` expire-ms (8-byte LE), else = value-type byte
  (`0` = string) then key then value.
- **Length encoding** by the first byte's top 2 bits: `00` = 6-bit len; `01` = 14-bit;
  `10` = `0x80`→4-byte BE / `0x81`→8-byte BE; `11` = special (0=int8, 1=int16 LE,
  2=int32 LE, 3=LZF). So "read a string" must handle the int-encoded forms.
- Expiry from the file is kept as an absolute timestamp; a key already past it is simply
  not served (`GET`→`$-1`, not listed by `KEYS`) — same lazy mechanism as `SET PX`.
- Missing RDB file = empty dataset, not an error.
- RDB is also what a master ships on full resync (ties back to group 6).

**Gotchas:** `--dir` unset should default to the process cwd (that's what `CONFIG GET dir`
returns). Tester passes a random `--dir`, so nothing is hardcodable.

---

## 8. AOF Persistence  *(in progress — 5 of 10 stages)*

**Done & submitted:** Default AOF options; AOF options from flags; Create append-only
directory; Create append-only file; Create manifest file.

**Remaining (code written in `app/aof.go`, not yet marked complete on the site):**
Write a single command; Write multiple commands; Filter write commands; Replay a single
command; Replay multiple commands.

**Key learnings so far**
- Defaults: `appendonly=no`, `appenddirname=appendonlydir`,
  `appendfilename=appendonly.aof`, `appendfsync=everysec`, `dir=cwd`. Flags override.
- With `appendonly yes` at startup: `mkdir -p <dir>/<appenddirname>/`, create empty
  `<appendfilename>.1.incr.aof`, create `<appendfilename>.manifest` containing
  `file <appendfilename>.1.incr.aof seq 1 type i\n` (single spaces, trailing newline).
- **Multi-part AOF (Redis 7+):** the dir holds a manifest + optional `.base.rdb` +
  `.incr.aof` parts. Manifest line: `file <name> seq <n> type <b|i>`. You must **read the
  manifest** to find the active `type i` file — the tester uses random names on purpose.
- Writes are appended in the same RESP array encoding used on the wire, verbatim.
  `appendfsync always` → `fsync` before replying to the client.
- Replay at startup: load `type b` parts as RDB, replay `type i` parts as a RESP command
  stream, guarded by a `replaying` flag so it doesn't re-append what it reads.
- `appendfsync`: `always` (~0 loss, fsync per write) / `everysec` (≤1s, bg thread) /
  `no` (OS decides). AOF grows unbounded → `BGREWRITEAOF` forks a compact new base.
- AOF and replication are the same idea (a write-command stream) — one to disk, one to
  replicas; both skip read-only commands.

**Checkpoint:** stop here for review before finishing the last 5 AOF stages and moving
to Pub/Sub.
