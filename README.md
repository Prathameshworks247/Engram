# Engram

A lightweight, Redis‑compatible in‑memory data store written from scratch in Go.

Engram speaks the [RESP](https://redis.io/docs/latest/develop/reference/protocol-spec/)
wire protocol, so you can talk to it with `redis-cli` or any Redis client library.
It implements the core of Redis — strings with expiry, lists, streams, transactions,
optimistic locking, leader/follower replication, and RDB/AOF persistence — with a
small, readable codebase (~1.9k lines).

> The name *engram* refers to a memory trace — the physical substrate of a stored
> memory. Fitting for a server whose whole job is holding your data in RAM.

---

## Features

### Core
- **RESP protocol** parser and encoder (arrays, bulk/simple strings, integers, errors, null)
- **Concurrent clients** — one goroutine per connection over a shared, mutex‑guarded store
- `PING`, `ECHO`
- `SET` / `GET` with `EX` (seconds) and `PX` (milliseconds) expiry; lazy expiration on access
- `TYPE`, `KEYS <pattern>` (glob: `*`, `?`)
- `CONFIG GET <param>`

### Lists
- `RPUSH`, `LPUSH` (multi‑element)
- `LRANGE` with inclusive bounds and negative indexes
- `LLEN`, `LPOP` (with optional count)
- `BLPOP` — blocking pop with timeout, across multiple keys

### Streams
- `XADD` with explicit, partial (`<ms>-*`) and fully auto‑generated (`*`) IDs, plus ID validation
- `XRANGE` with `-` / `+` bounds (inclusive)
- `XREAD` over one or many streams (exclusive lower bound)
- `XREAD BLOCK <ms>` including `BLOCK 0` (wait forever) and the `$` "from now on" ID

### Transactions & optimistic locking
- `MULTI` / `EXEC` / `DISCARD`, command queueing, per‑connection state
- `INCR`
- `WATCH` / `UNWATCH` — CAS via per‑key mutation counters; `EXEC` aborts (nil) if a watched key changed

### Replication (leader/follower)
- `--replicaof` to run as a replica; full `PSYNC` handshake (`PING` → `REPLCONF` → `PSYNC ? -1`)
- Master serves `+FULLRESYNC` + an empty RDB, then streams write commands to all replicas
- Replica applies the command stream and tracks its replication offset
- `REPLCONF GETACK` / `REPLCONF ACK <offset>`
- `WAIT <numreplicas> <timeout>`
- `INFO replication` (`role`, `master_replid`, `master_repl_offset`, …)

### Persistence
- **RDB**: loads `<dir>/<dbfilename>` at startup — header, aux/select/resize opcodes, string
  values (length‑ and integer‑encoded), and per‑key `EXPIRETIME` / `EXPIRETIMEMS`
- **AOF** (Redis 7 multi‑part): creates `<appenddirname>/` with an incremental log and a
  `manifest`; appends write commands in RESP form (honouring `appendfsync always`); on
  startup, replays the manifest's base (RDB) and incr (command‑log) parts

---

## Project layout

```
app/
  main.go             server bootstrap, command dispatch, CONFIG/KEYS
  resp.go             RESP parser + encoders
  store.go            key/value store: expiry, lists, streams, mutation counters
  client.go           per-connection state: MULTI/EXEC/WATCH, write fan-out
  commands_list.go    RPUSH/LPUSH/LRANGE/LLEN/LPOP/BLPOP
  stream.go           stream type + entry-ID logic
  commands_stream.go  XADD/XRANGE/XREAD
  config.go           command-line flag parsing
  replication.go      master/replica handshake, propagation, WAIT, ACK offsets
  rdb.go              RDB file reader
  aof.go              append-only file: manifest, write, replay
  errors.go           shared error strings
notes/                interview-oriented write-ups, one per feature area
```

---

## Getting started

Requires **Go 1.26+**.

```sh
# Build
go build -o /tmp/engram ./app

# Run (defaults: port 6379, no persistence, master role)
/tmp/engram

# …or use the helper script (build + run, forwards args)
./your_program.sh
```

Then, from another terminal:

```sh
redis-cli PING
# PONG
redis-cli SET course redis EX 60
# OK
redis-cli GET course
# "redis"
redis-cli RPUSH nums a b c
# (integer) 3
redis-cli LRANGE nums 0 -1
# 1) "a"  2) "b"  3) "c"
redis-cli XADD stream '*' temp 21
# "1724500000000-0"
```

### Configuration flags

| Flag | Purpose | Default |
|------|---------|---------|
| `--port <n>` | TCP port to listen on | `6379` |
| `--replicaof "<host> <port>"` | Run as a replica of the given master | — (master) |
| `--dir <path>` | Base directory for data files | current working dir |
| `--dbfilename <name>` | RDB file name (loaded at startup) | — |
| `--appendonly <yes\|no>` | Enable AOF persistence | `no` |
| `--appenddirname <name>` | AOF sub‑directory under `--dir` | `appendonlydir` |
| `--appendfilename <name>` | Base name for AOF files | `appendonly.aof` |
| `--appendfsync <always\|everysec\|no>` | AOF flush policy | `everysec` |

### Replication example

```sh
# terminal 1 — master
/tmp/engram --port 6379

# terminal 2 — replica
/tmp/engram --port 6380 --replicaof "localhost 6379"

# terminal 3
redis-cli -p 6379 SET k v
redis-cli -p 6380 GET k          # "v"  (propagated)
redis-cli -p 6379 WAIT 1 500     # (integer) 1
```

### Persistence example

```sh
# Load an existing RDB snapshot
/tmp/engram --dir /path/to/data --dbfilename dump.rdb

# Enable the append-only log
/tmp/engram --dir /path/to/data --appendonly yes --appendfsync always
```

---

## Testing

### CodeCrafters test suite

This project is built against the
[CodeCrafters "Build Your Own Redis"](https://app.codecrafters.io/courses/redis/overview)
challenge. Its stage tests spin up a real client (and, for replication, a real
master/replica pair) and assert on the wire protocol.

```sh
codecrafters test              # run the current stage's tests
codecrafters test --previous   # run the current stage + all previous stages
codecrafters submit            # commit, push, and run tests on CodeCrafters
```

### Manual testing with redis-cli

```sh
go build -o /tmp/engram ./app
/tmp/engram --port 6390 &
redis-cli -p 6390 SET a 1
redis-cli -p 6390 INCR a         # (integer) 2
redis-cli -p 6390 MULTI          # +OK
```

### Go build / vet

```sh
go build ./app
go vet ./app
```

---

## Tech used

- **Language:** Go 1.26 (standard library only — no third‑party dependencies)
- **Concurrency:** goroutine‑per‑connection, `sync.Mutex` / `sync.Cond` around the store
- **Networking:** `net` TCP listener, `bufio.Reader` for buffered protocol parsing
- **Protocol:** hand‑written RESP2 codec
- **Persistence:** custom RDB reader (`encoding/binary`), Redis‑7 multi‑part AOF writer/replayer
- **Test harness:** CodeCrafters stage tester (protocol‑level, black‑box)

---

## Notes

The [`notes/`](notes/) directory has a concise, interview‑focused write‑up for each
area (protocol, lists, streams, transactions, optimistic locking, replication,
RDB, AOF) plus a consolidated Q&A — what each feature does, how it's implemented
here, and the questions an interviewer tends to ask about it.
