# Optimistic Locking (WATCH/UNWATCH) — Interview Notes

## What the CodeCrafters stages asked you to build

1. **The WATCH command** — `WATCH key [key...]` → reply `+OK`. Start monitoring those keys.
2. **WATCH inside transaction** — calling `WATCH` after `MULTI` → error `ERR WATCH inside MULTI is not allowed`.
3. **Tracking key modifications** — if a watched key is changed (by anyone) between `WATCH` and `EXEC`, then `EXEC` runs **nothing** and replies with a null array `*-1\r\n`.
4. **Watching multiple keys** — any one of the watched keys changing aborts the transaction.
5. **Watching missing keys** — you can watch a key that doesn't exist yet; if it gets *created* before `EXEC`, that still counts as a modification → abort.
6. **The UNWATCH command** — `UNWATCH` → `+OK`, forget all watched keys; a later `EXEC` runs normally even if those keys changed.
7. **Unwatch on EXEC** — after `EXEC` (whether it ran or aborted) the connection's watch list is cleared.
8. **Unwatch on DISCARD** — `DISCARD` also clears the watch list.

## Mental model — CAS (compare-and-set)

`WATCH` + `MULTI` + `EXEC` = optimistic concurrency control:

```
WATCH balance
val = GET balance          # read
MULTI
SET balance (val - 100)    # queue the write based on what we read
EXEC                       # commit ONLY if `balance` didn't change since WATCH
```

- If nobody touched `balance`, `EXEC` commits and returns the results array.
- If someone did, `EXEC` returns `nil` (`*-1\r\n`) and the client **retries the whole read-modify-write loop**.
- "Optimistic" = assume no conflict, detect at commit, retry on failure. No key is ever locked; no other client is blocked.

## Implementation shape

Give the store a per-key mutation counter; bump it on **every write** (SET, INCR, RPUSH, LPOP, XADD, ...):

```go
versions map[string]uint64
func (s *Store) touch(key string) { s.versions[key]++ }   // called under lock by every mutator
```

Client records the counter at `WATCH` time:

```go
type Client struct { watching map[string]uint64 }

// WATCH k...  (only when not inMulti)
for _, k := range keys { c.watching[k] = store.Version(k) }

// EXEC
watched := c.watching; c.watching = nil          // EXEC always unwatches
if dirty(watched) { return "*-1\r\n" }            // some version changed
... run queued ...

func dirty(w map[string]uint64) bool {
    for k, v := range w { if store.Version(k) != v { return true } }
    return false
}
```
Watching a missing key works for free: its version is `0`; creating it calls `touch` → `1` → mismatch.

Real Redis does the same idea with a flag: each watched key has a list of clients watching it; any write to that key sets `CLIENT_DIRTY_CAS` on those clients, and `EXEC` checks the flag.

## Probable interview questions

**Q: Optimistic vs pessimistic locking?**
Pessimistic: take a lock before touching data, others wait (e.g. `SELECT ... FOR UPDATE`). Optimistic: don't lock, do the work, check at commit whether the data changed; retry if it did. Optimistic wins when conflicts are rare (less contention, no deadlocks); pessimistic wins when conflicts are common.

**Q: What exactly does `WATCH` lock?**
Nothing. It just registers interest. The only effect is that a concurrent modification makes the *next* `EXEC` on that connection fail.

**Q: How does the client know the transaction was aborted?**
`EXEC` returns a null array (`*-1\r\n`) instead of the results array. The client loops back and retries.

**Q: Does `WATCH` persist after `EXEC`?**
No — `EXEC`, `DISCARD`, and `UNWATCH` all clear the watch list. A dropped connection clears it too.

**Q: Can you `WATCH` after `MULTI`?**
No. `WATCH` must come before `MULTI`, otherwise `ERR WATCH inside MULTI is not allowed`. The read you're guarding has to happen before you start queueing.

**Q: Build an atomic counter / `INCR` with WATCH.**
`WATCH k; v=GET k; MULTI; SET k v+1; EXEC;` retry while `EXEC` returns nil. (In practice just use `INCR`, which is already atomic — see [[04-transactions]].)
