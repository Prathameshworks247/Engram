# AOF Persistence — Interview Notes

## What the CodeCrafters stages asked you to build

1. **Default AOF options** — `CONFIG GET` returns defaults: `appendonly`=`no`, `appenddirname`=`appendonlydir`, `appendfilename`=`appendonly.aof`, `appendfsync`=`everysec`, `dir`=cwd.
2. **AOF options from flags** — `--appendonly`, `--appenddirname`, `--appendfilename`, `--appendfsync`, `--dir` override the defaults.
3. **Create append-only directory** — at startup, if `appendonly yes`, `mkdir -p <dir>/<appenddirname>/` (don't create it otherwise).
4. **Create append-only file** — also create an empty `<appenddirname>/<appendfilename>.1.incr.aof`.
5. **Create manifest file** — create `<appenddirname>/<appendfilename>.manifest` containing one line: `file <appendfilename>.1.incr.aof seq 1 type i\n` (single spaces, trailing newline).
6. **Write a single command** — on a write command, **read the manifest** to find the active `type i` file, append the command in RESP array form. With `appendfsync always`, `fsync` before replying to the client.
7. **Write multiple commands** — same, for a stream of writes.
8. **Filter write commands** — only append mutating commands (`SET`, `DEL`, …); never `GET`, `PING`, etc.
9. **Replay a single command** — at startup, read the manifest and replay its files (base = RDB, incr = RESP log) into the store before serving clients.
10. **Replay multiple commands** — replay a full incr log.

## What AOF is

An **Append-Only File**: a log of every write command, in the same RESP encoding used on the wire. On restart Redis replays the log to rebuild memory. More durable than RDB (smaller loss window) at the cost of a bigger file and slower restart.

### `appendfsync` — the durability knob
| Value | Behavior | Loss window on crash |
|-------|----------|----------------------|
| `always` | fsync after every write, before ack | ~0 (a single in-flight write) |
| `everysec` (default) | fsync once per second from a background thread | up to 1 s |
| `no` | let the OS flush whenever | seconds–minutes |

## Multi-part AOF (Redis 7+)

Instead of one growing file, the `appendonlydir/` holds several parts described by a **manifest**:

```
appendonlydir/
  appendonly.aof.manifest
  appendonly.aof.1.base.rdb     # optional: snapshot of state at last rewrite (RDB format)
  appendonly.aof.1.incr.aof     # incremental: RESP commands since the base
```

Manifest lines: `file <name> seq <n> type <b|i>` — `b` = base (RDB), `i` = incr (command log). Recovery = load the base, then replay each incr in order. You must follow the manifest, not assume default names (the CodeCrafters tester deliberately uses random names).

### AOF rewrite / compaction
The incr log grows unbounded (`SET k 1`, `SET k 2`, … `SET k 999`). `BGREWRITEAOF` forks a child that writes a fresh, minimal base representing current state, starts a new incr file, and updates the manifest — bounding file size. Auto-triggered by `auto-aof-rewrite-percentage` / `auto-aof-rewrite-min-size`.

## Implementation shape

```go
func setupAOF() {
    if cfg.AppendOnly != "yes" { return }
    dir := filepath.Join(cfg.Dir, cfg.AppendDirname)
    os.MkdirAll(dir, 0o755)
    entries := parseManifest(read(dir + "/" + cfg.AppendFilename + ".manifest"))
    if entries == nil {                 // fresh start: create defaults
        write(incrFile, "");  write(manifest, "file "+incrName+" seq 1 type i\n")
        entries = [{incrName, "i"}]
    }
    replaying = true
    for _, e := range entries {
        if e.type == "b" { loadRDBFrom(dir+"/"+e.name) } else { replayRESP(dir+"/"+e.name) }
    }
    replaying = false
    aofFile = openAppend(dir + "/" + lastIncr(entries))
}

// in the command path, after executing a write command:
if isWriteCommand(cmd) && aofFile != nil && !replaying {
    aofFile.WriteString(encodeRESPArray(args))
    if cfg.AppendFsync == "always" { aofFile.Sync() }
}
```
The `replaying` guard stops the replay from re-appending what it just read. Note the write log stores the command **as received** (`SET k v`), so replay is just "run each command again."

## RDB vs AOF (recap — see [[07-rdb-persistence]])

| | RDB | AOF |
|---|---|---|
| Content | binary snapshot | command log (RESP) |
| Loss window | since last snapshot | since last fsync |
| Restart | fast | slower (replay) |
| Size | small | large, needs rewrite |

## Probable interview questions

**Q: RDB or AOF — which gives better durability, and why not always use it?**
AOF (`appendfsync everysec` loses ≤1 s; `always` loses ~nothing). You don't always prefer it alone because the file grows and needs periodic rewrites, and restart replay is slower than loading an RDB. Most run both.

**Q: What does `appendfsync everysec` actually guarantee?**
A background thread calls `fsync` on the AOF once per second, so a crash loses at most the last second of writes. `write()` still happens per command into the OS page cache; only the durable flush is batched.

**Q: Why did Redis 7 split the AOF into base + incr with a manifest?**
So a rewrite doesn't have to rewrite one huge file: the child writes a compact base (RDB) once, and new writes go to a fresh small incr file. The manifest records which parts exist and their replay order. Cheaper rewrites, less write amplification.

**Q: How does `BGREWRITEAOF` avoid blocking?**
`fork()`; the child serializes current state to a new base file using copy-on-write memory while the parent keeps serving and buffering new writes into the new incr file. On completion the manifest is swapped atomically.

**Q: A command is written to the AOF but the server crashes before fsync — what happens?**
With `everysec`/`no` that command is lost on restart (it was only in the page cache). With `always` it was fsynced before the client got its reply, so it survives. This is the durability/latency tradeoff.

**Q: Does the AOF store `SET k v EX 10` or the resolved absolute expiry?**
Redis rewrites relative-expiry commands to absolute (`PEXPIREAT`) in the AOF so that replay after an arbitrary delay yields the same expiry time. (The challenge stores commands verbatim, which is fine for its tests.)

**Q: How is AOF related to replication?**
Same core idea — a stream of write commands. Replication propagates that stream to replicas live (see [[06-replication]]); AOF persists it to disk. Both skip read-only commands.
