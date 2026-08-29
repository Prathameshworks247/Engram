# Redis Lists — Interview Notes

Redis lists = **linked lists of strings** (actually `quicklist`: a linked list of `listpack` nodes). O(1) push/pop at both ends, O(N) indexed access.

## Commands

| Command | Semantics | Reply |
|---------|-----------|-------|
| `RPUSH k v [v...]` | append to tail, create if missing | `:<new_len>` |
| `LPUSH k v [v...]` | prepend to head (each v goes to front → reverses input order) | `:<new_len>` |
| `LRANGE k start stop` | elements in range, **stop inclusive** | array of bulk strings |
| `LLEN k` | length (0 if missing) | `:<len>` |
| `LPOP k [count]` | pop `count` from head | bulk (no count) / array (with count) / null |
| `RPOP k [count]` | pop from tail | same shapes |
| `LREM`, `LINDEX`, `LSET`, `LTRIM`, `LINSERT` | other ops | |
| `BLPOP k [k...] timeout` | blocking LPOP; timeout in **seconds** (float, 0 = forever) | `[key, value]` or null array on timeout |

### Index normalization (LRANGE, LINDEX)
```go
if i < 0 { i += len }        // -1 = last element
if start < 0 { start = 0 }
if stop >= n { stop = n - 1 }
if start > stop || start >= n { return emptyArray }
```
- Empty/missing list → empty array `*0\r\n` (NOT null).
- `LPOP` missing → null bulk `$-1`; `LPOP k 0` or empty with count → empty array.
- When a list becomes empty after a pop, **delete the key** (Redis invariant: no empty aggregate keys).

## Blocking commands (BLPOP)

Two implementation strategies:

**1. Polling (used here):** release the lock, `time.Sleep(5ms)`, re-check keys until data or deadline. Simple, correct, ~ms latency.

```go
for {
    store.Lock()
    for _, k := range keys {
        if l, ok := list(k); ok && len(l.items) > 0 {
            v := pop(l); store.Unlock(); return [k, v]
        }
    }
    store.Unlock()
    if timeout > 0 && time.Now().After(deadline) { return nullArray }
    time.Sleep(5 * time.Millisecond)
}
```

**2. Condition variable:** `sync.Cond` tied to the store mutex. Every push does `cond.Broadcast()`. Waiters `cond.Wait()` (atomically unlocks + re-locks on wake). `sync.Cond` has **no timed wait** in Go — emulate with a timer goroutine that broadcasts at the deadline. More efficient, more code.

Real Redis: keeps a per-key list of blocked clients (`db->blocking_keys`); when a key is written it's added to `server.ready_keys` and served **FIFO** after the current command, so blocked clients are unblocked in arrival order and the pushing client's reply is sent first.

## Gotchas / interview points
- `LPUSH k a b c` → list is `c b a` (each element pushed to head).
- `LRANGE` stop is inclusive — differs from most language slice conventions.
- WRONGTYPE error if key holds a non-list: `-WRONGTYPE Operation against a key holding the wrong kind of value`.
- Blocking commands can't block the whole server — in single-threaded Redis they return control to the event loop and park the client.
- `BLPOP` with multiple keys checks them **left to right**; first non-empty wins.
