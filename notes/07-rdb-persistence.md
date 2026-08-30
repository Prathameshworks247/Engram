# RDB Persistence — Interview Notes

## What the CodeCrafters stages asked you to build

1. **RDB file config** — parse `--dir` and `--dbfilename`; implement `CONFIG GET dir` / `CONFIG GET dbfilename` → RESP array `[name, value]`.
2. **Read a key** — on startup, load `<dir>/<dbfilename>`; `KEYS *` returns the one key it contains.
3. **Read a string value** — `GET <key>` returns the value stored in the RDB.
4. **Read multiple keys** — `KEYS *` returns every key in the file.
5. **Read multiple string values** — `GET` works for each.
6. **Read value with expiry** — honour the per-key expire timestamps in the file; a key whose expiry is in the past is treated as missing (`GET` → `$-1`, not listed by `KEYS`).

## What an RDB file is

A **point-in-time binary snapshot** of the whole dataset. On startup Redis loads it; while running it periodically rewrites it (via `SAVE` / `BGSAVE`, or automatically per the `save` config like "every 60s if ≥1000 keys changed"). `BGSAVE` `fork()`s a child that writes the snapshot using copy-on-write memory, so the main thread keeps serving.

## File layout

```
"REDIS" + 4-digit version        e.g. REDIS0011
[ 0xFA  aux-key  aux-val ]  *     metadata (redis-ver, redis-bits, ...)
0xFE  <dbnum>                     SELECTDB
0xFB  <hashsize>  <expiressize>   RESIZEDB (hint for preallocation)
[ optional expire ]              0xFD + 4-byte LE seconds
                                 0xFC + 8-byte LE milliseconds
<value-type>  <key>  <value>     value-type 0 = string
...
0xFF                              EOF
<8-byte CRC64 checksum>
```

### Length encoding (first byte's top 2 bits)
| Bits | Meaning |
|------|---------|
| `00` | low 6 bits = length |
| `01` | low 6 bits + next byte = 14-bit length |
| `10` | `0x80` → next 4 bytes BE = length; `0x81` → next 8 bytes BE |
| `11` | **special**: low 6 bits pick 0=int8, 1=int16 LE, 2=int32 LE, 3=LZF-compressed |

So "read a string" = read a length-encoded number; if it was the special `11` form, decode the small integer instead of reading raw bytes.

### Parser skeleton
```go
read 9-byte header, check "REDIS"
loop:
  op = readByte()
  0xFF                -> done
  0xFE               -> readLength()            // select db, ignore
  0xFB               -> readLength(); readLength()   // resize hints, ignore
  0xFA               -> readString(); readString()   // aux, ignore
  0xFD               -> pendingExpireMs = readUint32LE()*1000
  0xFC               -> pendingExpireMs = readUint64LE()
  default (type byte) -> key=readString(); val=readString()
                         if type==0: store.SetAbsolute(key, val, pendingExpireMs)
                         pendingExpireMs = 0
```
Expiry is applied lazily on read (`now > expireAt` → delete + report missing), same mechanism as `SET ... PX` — see [[01-fundamentals]].

## RDB vs AOF

| | RDB | AOF |
|---|---|---|
| Format | binary snapshot | append log of write commands |
| Restart speed | fast (bulk load) | slower (replay commands) |
| Data loss window | since last snapshot (minutes) | since last fsync (`always` / `everysec` / `no`) |
| File size | compact | larger (grows; needs rewrite/compaction) |
| Use | backups, fast restarts, replication bootstrap | durability |

Typical production: **both enabled**. RDB is also what a master ships to a replica on full resync (see [[06-replication]]).

## Probable interview questions

**Q: What's in an RDB file and when is it written?**
A compressed binary image of every key/value (plus TTLs and some metadata), ending with a CRC64. Written by `SAVE` (blocking), `BGSAVE` (forked child, non-blocking), or automatically by `save <seconds> <changes>` rules.

**Q: How does `BGSAVE` avoid blocking the server?**
`fork()`; the child inherits a copy-on-write view of memory and serializes it while the parent keeps handling commands. Only pages the parent modifies during the save get physically copied.

**Q: RDB vs AOF — which would you pick?**
RDB for compact backups and fast restarts but a bigger loss window; AOF for minimal data loss. Most setups run both: AOF for durability, RDB for quick recovery and backups.

**Q: Why is the RDB length encoding variable-width?**
To keep the file small — most lengths fit in 6 bits (one byte), and the `11` prefix lets small integers (a very common value) be stored as 1–4 bytes instead of an ASCII string.

**Q: How are expired keys represented, and what happens on load?**
Each key may be preceded by `0xFC` (ms) or `0xFD` (seconds) giving an absolute Unix timestamp. On load you keep the timestamp; a key already past its expiry is simply not served (lazy deletion), so it effectively vanishes.

**Q: What's the CRC64 at the end for?**
Integrity check — Redis refuses to load a corrupted file (unless `rdbchecksum no` / `sanitize-dump-payload` settings say otherwise). A checksum of all zeros means checksumming was disabled when the file was written.
