# Authentication & ACLs — Interview Notes

## What the CodeCrafters stages asked you to build

1. **Respond to ACL WHOAMI** — `ACL WHOAMI` → bulk string `default`.
2. **Respond to ACL GETUSER** — `ACL GETUSER default` → `["flags", []]`.
3. **The nopass flag** — flags array becomes `["nopass"]` while the default user has no password.
4. **The passwords property** — reply grows to `["flags", ["nopass"], "passwords", []]`.
5. **Setting default user password** — `ACL SETUSER default >mypassword` → `+OK`; stores the **SHA‑256 hash** of the password and clears `nopass`. `ACL GETUSER` then lists the hash under `passwords`.
6. **The AUTH command** — `AUTH <user> <pass>` → `+OK` if the password's SHA‑256 matches one in the user's list, else `WRONGPASS invalid username-password pair or user is disabled.`
7. **Enforce authentication** — once the default user has a password, a **new** connection is unauthenticated: any command except `AUTH`/`HELLO`/`RESET` → `NOAUTH Authentication required.` Connections opened *before* the password was set stay authenticated.
8. **Authenticate using AUTH** — after a successful `AUTH`, that connection can run commands again. `AUTH <pass>` (one arg) authenticates as `default`.

## Mental model

Every connection has an **identity** (a user) and an **auth state**. The `default` user exists from boot. Its `nopass` flag means "any/no password authenticates," which is why fresh connections are usable immediately on a stock Redis.

- `ACL SETUSER default >pw` adds a password hash and drops `nopass` → auth is now enforced for *new* connections.
- `AUTH` moves a connection from unauthenticated → authenticated by proving knowledge of a password.
- Passwords are stored **only as SHA‑256 hex hashes** — never plaintext — so a dump of the ACL config doesn't leak credentials. `>pw` hashes for you; `#<hexhash>` adds a pre-computed hash.

### The "already-connected stays logged in" subtlety
Enforcement is per-connection and evaluated against the connection's *own* auth flag, not recomputed globally. Implementation: stamp `client.authed = !authRequired()` **at connect time**. When a password is later set, existing clients keep `authed = true`; new clients get `authed = false` and hit `NOAUTH`.

```go
// on accept:
c.authed = !authRequired()   // authRequired() == default user has a password

// per command:
if authRequired() && !c.authed {
    if cmd not in {AUTH, HELLO, RESET, QUIT} {
        return "-NOAUTH Authentication required."
    }
}

// AUTH:
if sha256(pw) in defaultUser.passwords || defaultUser.nopass {
    c.authed = true
} else {
    return "-WRONGPASS ..."
}
```

## Redis ACLs beyond this challenge

Real ACL rules (`ACL SETUSER alice on >pw ~cache:* +get +set -flushall &news:*`):
- `on`/`off` — enable/disable the user.
- `~pattern` / `%RW~pattern` / `allkeys` — which keys, and R/W.
- `+cmd` / `-cmd` / `+@category` / `allcommands` — which commands (categories like `@read`, `@dangerous`).
- `&channel` / `allchannels` — pub/sub channels.
- `>pw` add password, `<pw` remove, `#hash` add hash, `nopass`, `resetpass`.
- `selectors` — alternate permission sets for the same user.
`ACL WHOAMI`, `ACL LIST`, `ACL CAT`, `ACL GETUSER`, `ACL DELUSER`, `ACL SAVE`/`LOAD` (to an `aclfile`).

## Probable interview questions

**Q: How are Redis passwords stored?**
As SHA‑256 hex hashes in the user's `passwords` list — never plaintext. `ACL SETUSER u >secret` hashes `secret` before storing; `AUTH` hashes the supplied password and compares.

**Q: What is the `nopass` flag and why does the default user have it?**
`nopass` means authentication always succeeds regardless of password. The `default` user ships with it so a fresh Redis is immediately usable. `requirepass` / `ACL SETUSER default >pw` removes it.

**Q: A password is set on the default user. What happens to connections that were already open?**
They stay authenticated — enforcement checks each connection's existing auth flag, and they were auto‑authed at connect time. Only connections opened *after* the password is set start out unauthenticated and must `AUTH`.

**Q: `requirepass` vs the ACL system?**
`requirepass foo` is the legacy single‑password knob; internally it's just `ACL SETUSER default >foo`. The ACL system generalises it to many users, per‑command/key/channel permissions, and multiple passwords per user.

**Q: `AUTH` with one argument vs two?**
`AUTH <password>` authenticates as `default` (legacy form). `AUTH <username> <password>` authenticates as a named ACL user (Redis 6+).

**Q: What error does an unauthenticated client get, and which commands are still allowed?**
`NOAUTH Authentication required.` Only `AUTH`, `HELLO` (which can carry `AUTH`), and `RESET` are permitted before authenticating.

**Q: Wrong password error?**
`WRONGPASS invalid username-password pair or user is disabled.` — a RESP simple error; the same message whether the user is unknown, the password is wrong, or the user is `off`, to avoid leaking which.
