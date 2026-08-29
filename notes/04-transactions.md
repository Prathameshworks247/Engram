# Redis Transactions (MULTI/EXEC) — Interview Notes

## What the CodeCrafters stages asked you to build

1. **INCR (1/3, 2/3, 3/3)** — `INCR key`: +1 to an integer string.
   - key holds a number → increment, reply `:<new>`
   - key missing → treat as 0, set to `1`
   - key holds non-number → error `ERR value is not an integer or out of range`
2. **MULTI** — reply `+OK`, mark the connection "in transaction".
3. **EXEC without MULTI** — error `ERR EXEC without MULTI`.
4. **Empty transaction** — `MULTI` then `EXEC` → empty array `*0\r\n`.
5. **Queueing commands** — after `MULTI`, every normal command is stored, not run, and replies `+QUEUED`.
6. **Executing a transaction** — `EXEC` runs the queued commands in order and replies with an **array of their individual replies**.
7. **DISCARD** — throw away the queue, reply `+OK`; `DISCARD without MULTI` is an error.
8. **Failures within transactions** — a queued command that errors at run time (e.g. `INCR` on a string) puts its error *inside* the results array; the other queued commands still run.
9. **Multiple transactions** — transaction state is **per connection**; two clients each have their own MULTI/queue.

## Mental model

A Redis transaction is **not** a rollback transaction. It is:
- **Atomic execution**: all queued commands run back-to-back with nothing interleaved (single-threaded server).
- **No rollback**: if command #3 fails, #1, #2, #4 still applied. Redis treats in-transaction runtime errors as programmer bugs.

Two error timings:
| When | Example | Effect |
|------|---------|--------|
| **Queue time** | unknown command, wrong arg count | reply error now, mark tx dirty → `EXEC` returns `EXECABORT` and runs nothing |
| **Run time** | `INCR` on non-number | error goes into the results array at `EXEC`, rest still run |

(The challenge only tests the run-time case.)

## Implementation shape

Per-connection state on the client object:
```go
type Client struct {
    inMulti  bool
    queue    [][]string
}

func (c *Client) dispatch(args) string {
    if c.inMulti {
        switch cmd {
        case "EXEC":    return c.execTransaction()
        case "DISCARD": c.inMulti=false; c.queue=nil; return "+OK"
        case "MULTI":   return "-ERR MULTI calls can not be nested"
        default:        c.queue=append(c.queue,args); return "+QUEUED"
        }
    }
    switch cmd {
    case "MULTI": c.inMulti=true; return "+OK"
    case "EXEC":  return "-ERR EXEC without MULTI"
    case "DISCARD":return "-ERR DISCARD without MULTI"
    }
    return execCommand(args) // normal path
}

func (c *Client) execTransaction() string {
    c.inMulti = false
    q := c.queue; c.queue = nil
    parts := make([]string, len(q))
    for i, a := range q { parts[i] = execCommand(a) }  // each returns a full RESP reply
    return "*"+len(q)+"\r\n" + concat(parts)           // array of raw replies
}
```
The neat trick: each command handler already returns a complete RESP string, so an "array of replies" is just `*N\r\n` + concatenation.

## Probable interview questions

**Q: Does Redis roll back a transaction if one command fails?**
No. Queued-command runtime errors don't abort the others; there is no rollback. Only a malformed command detected at queue time makes `EXEC` refuse to run anything (`EXECABORT`).

**Q: How is atomicity achieved without locks?**
The server is single-threaded. Once `EXEC` starts, the queued commands execute consecutively in the event loop with no other client's command in between.

**Q: What does `MULTI`/`EXEC` return?**
`MULTI` → `+OK`. Each subsequent command → `+QUEUED`. `EXEC` → array with one entry per queued command, each being that command's normal reply.

**Q: Why would you use `WATCH` with `MULTI`?**
To get compare-and-set / optimistic concurrency — see [[05-optimistic-locking]].

**Q: `MULTI` then `INCR` on a WRONGTYPE key — when do you find out?**
At `EXEC`: the array contains the error for that slot; siblings still executed.

**Q: Difference between a transaction and a Lua script / `FUNCTION`?**
Both run atomically. Scripts allow logic/conditionals and see intermediate results; transactions are a fixed pre-declared list with no branching.
