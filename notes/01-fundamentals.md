# Redis Fundamentals — Interview Notes

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

## Likely interview questions
- *Why is Redis fast despite being single-threaded?* In-memory, no lock contention, efficient data structures, multiplexed non-blocking I/O, simple protocol.
- *How does RESP framing work / how do you know a message is complete?* Length prefixes on arrays and bulk strings.
- *How does key expiry work?* Lazy on access + active random sampling; TTL stored as absolute time.
