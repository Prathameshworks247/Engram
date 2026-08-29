# Redis Fundamentals — Interview Notes

## What the CodeCrafters stages asked you to build

1. **Bind to a port** — open a TCP listener on `6379`.
2. **Respond to PING** — reply `+PONG\r\n` to one `PING`.
3. **Respond to multiple PINGs** — loop: keep reading commands on the same connection and replying.
4. **Handle concurrent clients** — serve many connections at once (goroutine per connection).
5. **ECHO** — `ECHO hey` → bulk string `hey`.
6. **SET & GET** — `SET k v` → `+OK`; `GET k` → bulk value, or `$-1\r\n` if missing.
7. **Expiry** — `SET k v PX <ms>` / `EX <s>`; after the TTL, `GET` returns null.

Everything after this stage is "add one more command to the same read-loop + dispatch".

## 1. TCP server basics (Go)

```go
l, _ := net.Listen("tcp", "0.0.0.0:6379") // listener socket
for {
    conn, _ := l.Accept()   // blocks until a client connects
    go handleConn(conn)     // one goroutine per connection => concurrency
}
```

- **Listener** = passive socket bound to a port. `Accept()` returns a new **connection socket** per client.
- **Concurrent clients**: goroutine-per-connection is the idiomatic Go model. Real Redis is single-threaded with an event loop (epoll/kqueue) — same external behavior, different mechanism.
- Always `defer conn.Close()`. Read loop exits on `io.EOF` when the client disconnects.

## 2. RESP (REdis Serialization Protocol)

Request from client is **always an array of bulk strings**:

```
*2\r\n$4\r\nECHO\r\n$3\r\nhey\r\n
```

| Type          | Prefix | Example              | Meaning                     |
|---------------|--------|---------------------|-----------------------------|
| Simple string | `+`    | `+OK\r\n`            | short status                |
| Error         | `-`    | `-ERR msg\r\n`       | error                       |
| Integer       | `:`    | `:42\r\n`            | 64-bit int                  |
| Bulk string   | `$`    | `$3\r\nfoo\r\n`      | binary-safe string          |
| Null bulk     | `$-1\r\n` |                  | key missing                 |
| Array         | `*`    | `*2\r\n...`          | length-prefixed list        |

Parser sketch:

```go
line, _ := reader.ReadString('\n')      // read up to \n
n := atoi(line[1:])                     // *N  -> N elements
for i := 0; i < n; i++ {
    hdr, _ := reader.ReadString('\n')   // $len
    buf := make([]byte, atoi(hdr[1:])+2)
    io.ReadFull(reader, buf)            // exact bytes + trailing \r\n
    args = append(args, string(buf[:len-... ]))
}
```

Key point: **length-prefixed**, so it's binary-safe and you never guess where a value ends. Use `bufio.Reader` for buffered reads; `io.ReadFull` for the exact payload.

## 3. Commands implemented

| Command | Reply | Notes |
|---------|-------|-------|
| `PING`  | `+PONG` or bulk echo of arg | |
| `ECHO x`| bulk string `x` | |
| `SET k v [EX s\|PX ms]` | `+OK` | store value + optional expiry |
| `GET k` | bulk value / `$-1` | lazy-expire on read |

## 4. Key expiry

- Store `expireAt time.Time` per entry; zero = never.
- **Passive/lazy expiration**: on `GET`, if `now > expireAt`, delete and return null. (Real Redis also does *active* sampling via a background cycle.)
- `EX` = seconds, `PX` = milliseconds. `SET` also supports `EXAT/PXAT` (absolute), `NX/XX`, `KEEPTTL`, `GET`.

## 5. Concurrency correctness

- Shared map needs a `sync.Mutex` (or `sync.RWMutex`). Every read/write of `data` takes the lock.
- Later blocking commands (BLPOP, XREAD BLOCK, WAIT) need a `sync.Cond` on the same mutex so writers can wake waiting readers.

## Probable interview questions

**Q: Why is Redis so fast if it's single-threaded?**
Everything is in RAM; no lock contention because one thread owns the data; O(1)/O(log n) data structures; I/O multiplexing (epoll/kqueue) so one thread juggles thousands of sockets; a tiny, length-prefixed wire protocol that's cheap to parse.

**Q: How does the RESP protocol frame messages — how do you know a command is complete?**
Every aggregate is length-prefixed: `*3` means 3 elements follow, `$4` means the next 4 bytes are the string. You never scan for a delimiter, you read exactly the number of bytes announced. That makes it binary-safe (values can contain `\r\n`).

**Q: A client sends `PING` then `PING` on one connection — how does the server handle it?**
The per-connection handler is a loop: parse one full RESP command, write its reply, go back and parse the next. TCP is a byte stream, so the buffered reader may already hold the second command; the length prefixes tell it where command 1 ends.

**Q: How does key expiry work internally?**
TTL is stored as an absolute timestamp on the entry. **Lazy**: on every access, if `now > expireAt`, delete and treat as missing. **Active**: a background cycle samples random keys with TTLs ~10×/sec and evicts expired ones so dead keys don't accumulate.

**Q: Goroutine-per-connection — does real Redis do that?**
No. Real Redis is a single-threaded event loop. Same observable behavior; Go's model is just easier to write. (Redis 6+ added threaded *I/O* for socket read/write, but command execution stays single-threaded.)

**Q: What's the difference between a simple string (`+OK`) and a bulk string (`$2\r\nOK`)?**
Simple strings are short, no-newline status/OK responses. Bulk strings are length-prefixed and binary-safe — used for actual data values. `GET` returns bulk; `SET` returns simple.
