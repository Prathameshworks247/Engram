# Redis Lists — Interview Notes

## What the CodeCrafters stages asked you to build

1. **Create a list** — `RPUSH key el` on a missing key creates it; reply `:1`.
2. **Append an element** — `RPUSH` on an existing list; reply new length.
3. **Append multiple elements** — `RPUSH key a b c`.
4. **List elements (positive indexes)** — `LRANGE key 0 2` (stop inclusive).
5. **List elements (negative indexes)** — `LRANGE key -3 -1` (from the end).
6. **Prepend elements** — `LPUSH key a b c` (result is `c b a`).
7. **Query list length** — `LLEN key` (0 if missing).
8. **Remove an element** — `LPOP key` → one element / null.
9. **Remove multiple elements** — `LPOP key 2` → array.
10. **Blocking retrieval** — `BLPOP key 0` blocks until something is pushed.
11. **Blocking retrieval with timeout** — `BLPOP key 0.1` returns null array after the timeout.

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

## Probable interview questions

**Q: `LPUSH mylist a b c` — what's the final list order?**
`c b a`. Each element is pushed to the head one at a time, so the last argument ends up first. `RPUSH` keeps input order.

**Q: Is `LRANGE 0 -1` inclusive of the last element?**
Yes. Both bounds are inclusive and negative indexes count from the end (`-1` = last). `LRANGE key 0 -1` returns the whole list.

**Q: How is a Redis list stored? Is it a real linked list?**
Logically yes; physically it's a **quicklist** — a doubly-linked list whose nodes are `listpack`s (compact arrays of a few dozen elements). This keeps O(1) ends while avoiding one allocation + two pointers per element.

**Q: How does `BLPOP` block without freezing the server?**
Single-threaded Redis can't actually sleep. It parks the client: registers it in a per-key wait list and returns to the event loop. When a `PUSH` makes the key ready, the server serves the blocked clients (FIFO) right after the current command. A from-scratch implementation can just poll with a short sleep.

**Q: `BLPOP a b c 0` — which key does it pop from?**
It checks `a`, `b`, `c` left to right and pops from the first non-empty one. `0` timeout means block forever.

**Q: What happens to a list key when its last element is popped?**
The key is deleted. Redis never keeps an empty list/set/hash/zset — `EXISTS` goes back to 0.

**Q: When would you use a Redis list?**
Simple queues / job queues (`LPUSH` + `BRPOP`), capped activity feeds (`LPUSH` + `LTRIM`), or a lightweight stack. For anything needing consumer groups or replay, use a Stream instead — see [[03-streams]].
