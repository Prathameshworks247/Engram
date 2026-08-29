# Redis Replication (Leader–Follower) — Interview Notes

## What the CodeCrafters stages asked you to build

**Config & role**
1. **Configure listening port** — parse `--port <n>`, bind there (default 6379).
2. **The INFO command** — `INFO replication` → bulk string containing `role:master`.
3. **The INFO command on a replica** — parse `--replicaof "<host> <port>"`; when set, `INFO` reports `role:slave`.
4. **Initial replication ID and offset** — master's INFO also has `master_replid:<40 hex chars>` and `master_repl_offset:0`.

**Replica → master handshake (replica side)**
5. **Send handshake (1/3)** — replica dials master, sends `PING` (RESP array).
6. **Send handshake (2/3)** — sends `REPLCONF listening-port <port>`, then `REPLCONF capa psync2`.
7. **Send handshake (3/3)** — sends `PSYNC ? -1`.

**Master side of handshake**
8. **Receive handshake (1/2)** — master replies `+OK` to each `REPLCONF`.
9. **Receive handshake (2/2)** — master replies `+FULLRESYNC <replid> 0\r\n` to `PSYNC ? -1`.
10. **Empty RDB transfer** — master then sends `$<len>\r\n<binary RDB bytes>` (no trailing CRLF). A hardcoded empty RDB is fine.

**Propagation**
11. **Single-replica propagation** — after the handshake, forward every *write* command the master receives (as a RESP array) to the replica; non-writes (PING/GET) are not forwarded.
12. **Multi-replica propagation** — forward to *all* connected replicas.
13. **Command processing** — the replica applies commands received on the replication connection, and sends **no reply** for them.

**ACKs & WAIT**
14. **ACKs with no commands** — replica replies to `REPLCONF GETACK *` with `REPLCONF ACK 0` (hardcoded).
15. **ACKs with commands** — replica tracks an **offset** = total bytes of commands received from master. On `GETACK`, reply `REPLCONF ACK <offset>` where offset counts only commands **before** this GETACK, then add the GETACK's own bytes.
16. **WAIT with no replicas** — `WAIT 0 <timeout>` → `:0`.
17. **WAIT with no commands** — if no write has been propagated yet, `WAIT` returns the count of connected replicas immediately.
18. **WAIT with multiple commands** — master sends `REPLCONF GETACK *` to every replica, counts how many reply with `ACK offset >= masterOffset` (measured before the GETACK), and returns that count once it reaches `numreplicas` **or** the timeout expires.

## Architecture

```
                 writes          REPLCONF GETACK *
   client ───────────────► MASTER ──────────────► REPLICA
                            │  ▲                    │
                    propagate│  │REPLCONF ACK <off> │ applies commands
                            ▼  │                    ▼
                        [replica conns]          [same global store]
```

- **master_replid**: 40 hex chars, random at boot; identifies the master's data history.
- **master_repl_offset**: running byte count of everything the master has streamed to replicas.
- A replica is just a normal client connection that sent `PSYNC`; after that the master keeps writing propagated commands to it and reads back only `REPLCONF ACK`.

### Handshake (4 round trips)
```
replica → PING                          master → +PONG
replica → REPLCONF listening-port 6380   master → +OK
replica → REPLCONF capa psync2           master → +OK
replica → PSYNC ? -1                     master → +FULLRESYNC <replid> 0
                                         master → $<len>\r\n<RDB bytes>
                                         master → (then a live command stream)
```
`?` = "I don't know the replid", `-1` = "I have no data". This forces a **full resync** (send the whole dataset as RDB). A real replica with recent data can ask for a **partial resync** from a backlog buffer.

### Offset accounting (the fiddly part)
- Replica: `offset += len(RESP-encoding of command)` for **every** command from master (PING, SET, REPLCONF GETACK…).
- Rule: reply to `GETACK` with the offset **excluding** the current GETACK, then add its bytes.
- `WAIT numreplicas timeout`:
  - snapshot `target = master_repl_offset`
  - if `target == 0` → reply `:len(replicas)` (nothing to wait for)
  - else send `REPLCONF GETACK *` to all replicas, then poll their reported ack offsets
  - return count of `ackOffset >= target` as soon as it hits `numreplicas`, or when `timeout` ms elapse (whichever first)

## Consistency model

Redis replication is **asynchronous**: the master replies to the client *before* replicas confirm. So it's eventually consistent and you can lose the last few writes on failover. `WAIT n ms` gives **tunable** stronger durability — block until `n` replicas ack or timeout — but it's not a true synchronous commit (the write already happened on the master).

## Probable interview questions

**Q: Walk me through what happens when a replica connects.**
TCP connect → 4-step handshake (PING, REPLCONF ×2, PSYNC) → master sends `+FULLRESYNC replid offset` then an RDB snapshot → replica loads the snapshot → master streams every subsequent write command on the same socket → replica applies them and only answers `REPLCONF GETACK`.

**Q: Is replication synchronous or asynchronous?**
Asynchronous by default. The master doesn't wait for replicas. `WAIT` lets a client block for N acks, trading latency for durability, but the write is already committed on the master regardless.

**Q: What are the replication ID and offset for?**
The ID identifies the master's dataset lineage; the offset is a byte cursor into the replication stream. Together `(replid, offset)` let a reconnecting replica ask "continue me from here" — a **partial resync** from the master's backlog buffer instead of a full RDB transfer.

**Q: Full resync vs partial resync?**
Full = master dumps the entire dataset as RDB (on first sync or when the backlog can't cover the gap). Partial = master replays only the missing bytes from an in-memory circular backlog buffer (`PSYNC <replid> <offset>`), much cheaper after a brief disconnect.

**Q: Why must the replica NOT reply to propagated commands?**
The replication socket is one-directional for data; the only thing the replica writes back is `REPLCONF ACK`. If it echoed `+OK`/`+PONG` for propagated `SET`/`PING`, those bytes would corrupt the master's parsing of ACKs (classic bug).

**Q: How does `WAIT` actually work?**
Master records its current offset, sends `REPLCONF GETACK *` to replicas, and counts replicas whose returned ACK offset ≥ that recorded offset. Returns when the requested count is reached or the timeout fires.

**Q: What happens on master failure?**
Redis Sentinel (or Cluster) detects it and promotes a replica. Un-acked writes on the old master are lost — that's the async tradeoff. Sentinel handles monitoring, notification, automatic failover, and acts as a config provider to clients.

**Q: How would you scale reads?**
Point read traffic at replicas (`READONLY` on Cluster, or just connect to replica nodes). Writes still funnel through the single master — that's the write bottleneck that Redis Cluster's sharding addresses.
