# Pub/Sub — Interview Notes

## What the CodeCrafters stages asked you to build

1. **Subscribe to a channel** — `SUBSCRIBE ch` → array `["subscribe", ch, <count>]` (count = channels this connection is subscribed to).
2. **Subscribe to multiple channels** — one such array per channel; count increments; re‑subscribing the same channel doesn't bump the count.
3. **Enter subscribed mode** — once subscribed, only `SUBSCRIBE`, `UNSUBSCRIBE`, `PSUBSCRIBE`, `PUNSUBSCRIBE`, `PING`, `QUIT`, `RESET` are allowed; anything else → `ERR Can't execute '<cmd>': only (P|S)SUBSCRIBE / (P|S)UNSUBSCRIBE / PING / QUIT / RESET are allowed in this context`.
4. **PING in subscribed mode** — reply is **not** `+PONG` but the array `["pong", ""]` (or `["pong", <arg>]`).
5. **Publish a message** — `PUBLISH ch msg` → integer = number of subscribers on `ch`.
6. **Deliver messages** — each subscriber of `ch` is pushed the array `["message", ch, msg]` on its own connection.
7. **Unsubscribe** — `UNSUBSCRIBE ch` → `["unsubscribe", ch, <remaining count>]`; no‑arg `UNSUBSCRIBE` drops all.

## Mental model

Pub/Sub is **fire‑and‑forget broadcast**:
- No persistence, no history, no acknowledgement.
- A message goes only to clients **currently** subscribed. Anyone offline or subscribing a millisecond later misses it.
- Decouples senders from receivers — the publisher doesn't know or care who's listening.

Contrast with Streams (durable log, replay, consumer groups) — see [[03-streams]] — and Lists as queues — see [[02-lists]].

### Subscribed mode
After the first `SUBSCRIBE`, a RESP2 connection is "in subscribed mode": it can't run normal commands, because the connection is now a one‑way push channel for `message` frames. The client library switches to a read loop. `RESET` / `UNSUBSCRIBE`-to-zero returns it to normal.

## Implementation shape

```go
// global registry
var pubsub struct {
    mu       sync.Mutex
    channels map[string]map[*Client]bool   // channel -> set of subscribers
}

// per client
type Client struct {
    writeMu sync.Mutex          // serialises socket writes
    subs    map[string]bool     // channels this client is on
}
func (c *Client) send(b []byte) { c.writeMu.Lock(); c.conn.Write(b); c.writeMu.Unlock() }

// SUBSCRIBE ch...
for _, ch := range channels {
    pubsub.channels[ch][c] = true
    c.subs[ch] = true
    reply(["subscribe", ch, len(c.subs)])
}

// PUBLISH ch msg
snapshot subscribers of ch under the lock
frame := encode(["message", ch, msg])
for _, sub := range subscribers { sub.send(frame) }   // NOT under pubsub.mu
return len(subscribers)

// on disconnect: remove c from every channel it was in
```

**The key concurrency point:** the publisher's goroutine writes directly into *other* clients' sockets. So (a) every socket write goes through a per‑client `writeMu` so a delivered `message` frame can't interleave with that client's own command reply, and (b) you snapshot the subscriber set under `pubsub.mu` and then release it before doing the writes, so a slow/blocked subscriber can't stall `PUBLISH` while holding the global lock.

## Probable interview questions

**Q: Pub/Sub vs Streams vs a List — when would you use each?**
Pub/Sub: real‑time broadcast where missing a message is OK (live dashboards, cache invalidation fan‑out). List (`LPUSH`+`BRPOP`): simple work queue, one worker per job, no replay. Stream: durable event log, multiple independent consumers, replay from any point, consumer groups for load‑balanced at‑least‑once processing.

**Q: What happens to messages published to a channel with no subscribers?**
They're dropped. `PUBLISH` returns `0`. There is no buffering.

**Q: Why can't a subscribed client run `GET`?**
In RESP2 the connection has become a unidirectional stream of push frames; mixing request/response commands would make replies ambiguous against incoming `message` frames. RESP3 lifts this with a dedicated push type, so RESP3 clients can subscribe and still issue commands.

**Q: How does `PUBLISH` deliver to subscribers on other connections?**
The server holds a `channel -> set<connection>` map. `PUBLISH` looks up the set and writes the `["message", channel, payload]` array to each of those sockets. Writes must be synchronised per connection so they don't interleave with that connection's other output.

**Q: Does Pub/Sub work across a Redis Cluster?**
Plain `PUBLISH`/`SUBSCRIBE` is broadcast to the whole cluster (every node), which doesn't scale. **Sharded Pub/Sub** (`SPUBLISH`/`SSUBSCRIBE`, Redis 7) confines a channel to the shard that owns its key slot, so it scales horizontally.

**Q: How is Pub/Sub related to replication?**
Both are "push a stream of things to interested parties." Redis actually propagates `PUBLISH` to replicas so that clients subscribed on a replica also receive messages. Neither persists anything. See [[06-replication]].

**Q: Keyspace notifications?**
An opt‑in feature (`notify-keyspace-events`) where Redis itself publishes to `__keyspace@0__:<key>` / `__keyevent@0__:<event>` channels when keys change — lets clients react to expirations, deletes, etc. without polling.
