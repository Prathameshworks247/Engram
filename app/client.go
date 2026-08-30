package main

import (
	"net"
	"strconv"
	"strings"
	"sync"
)

// Client holds per-connection state (transactions, watched keys, etc.).
type Client struct {
	conn     net.Conn
	writeMu  sync.Mutex // serialises writes (reply loop + pub/sub delivery)
	inMulti  bool
	queue    [][]string
	watching map[string]uint64 // key -> store version at WATCH time
	replica  *Replica          // non-nil once this connection issued PSYNC
	subs     map[string]bool   // channels this client is subscribed to
}

// send writes raw bytes to the connection under the write lock.
func (c *Client) send(b []byte) {
	c.writeMu.Lock()
	c.conn.Write(b)
	c.writeMu.Unlock()
}

func (c *Client) subscribed() bool { return len(c.subs) > 0 }

func (c *Client) dispatch(args []string) string {
	cmd := strings.ToUpper(args[0])

	switch cmd {
	case "REPLCONF":
		if len(args) >= 3 && strings.ToUpper(args[1]) == "ACK" {
			if c.replica != nil {
				n, _ := strconv.ParseInt(args[2], 10, 64)
				c.replica.mu.Lock()
				c.replica.ackOffset = n
				c.replica.mu.Unlock()
			}
			return "" // ACK gets no reply
		}
		return encodeSimpleString("OK")
	case "PSYNC":
		resync := "+FULLRESYNC " + masterReplID + " 0\r\n"
		rdb := emptyRDBBytes()
		c.conn.Write([]byte(resync + "$" + strconv.Itoa(len(rdb)) + "\r\n"))
		c.conn.Write(rdb)
		c.replica = registerReplica(c.conn)
		return ""
	case "SUBSCRIBE":
		return c.cmdSubscribe(args)
	case "UNSUBSCRIBE":
		return c.cmdUnsubscribe(args)
	case "PUBLISH":
		return cmdPublish(args)
	}

	if c.subscribed() && !allowedInSubscribeMode(cmd) {
		return encodeError("ERR Can't execute '" + strings.ToLower(cmd) +
			"': only (P|S)SUBSCRIBE / (P|S)UNSUBSCRIBE / PING / QUIT / RESET are allowed in this context")
	}
	if c.subscribed() && cmd == "PING" {
		payload := ""
		if len(args) >= 2 {
			payload = args[1]
		}
		return encodeArray([]string{
			encodeBulkString("pong"),
			encodeBulkString(payload),
		})
	}

	if c.inMulti {
		switch cmd {
		case "EXEC":
			return c.execTransaction()
		case "DISCARD":
			c.inMulti = false
			c.queue = nil
			c.watching = nil
			return encodeSimpleString("OK")
		case "MULTI":
			return encodeError("ERR MULTI calls can not be nested")
		case "WATCH":
			return encodeError("ERR WATCH inside MULTI is not allowed")
		default:
			c.queue = append(c.queue, args)
			return encodeSimpleString("QUEUED")
		}
	}

	switch cmd {
	case "MULTI":
		c.inMulti = true
		c.queue = nil
		return encodeSimpleString("OK")
	case "EXEC":
		return encodeError("ERR EXEC without MULTI")
	case "DISCARD":
		return encodeError("ERR DISCARD without MULTI")
	case "WATCH":
		if len(args) < 2 {
			return encodeError("ERR wrong number of arguments for 'watch' command")
		}
		if c.watching == nil {
			c.watching = make(map[string]uint64)
		}
		for _, k := range args[1:] {
			c.watching[k] = store.Version(k)
		}
		return encodeSimpleString("OK")
	case "UNWATCH":
		c.watching = nil
		return encodeSimpleString("OK")
	}

	reply := execCommand(args)
	if isWriteCommand(cmd) {
		appendAOF(args)
		if !cfg.isReplica() {
			propagate(args)
		}
	}
	return reply
}

func (c *Client) execTransaction() string {
	c.inMulti = false
	queued := c.queue
	c.queue = nil
	watched := c.watching
	c.watching = nil // EXEC always unwatches

	if c.dirty(watched) {
		return encodeNullArray()
	}

	parts := make([]string, len(queued))
	for i, args := range queued {
		parts[i] = execCommand(args)
	}
	return encodeArray(parts)
}

// dirty reports whether any watched key changed since it was watched.
func (c *Client) dirty(watched map[string]uint64) bool {
	if len(watched) == 0 {
		return false
	}
	store.Lock()
	defer store.Unlock()
	for k, v := range watched {
		if store.VersionLocked(k) != v {
			return true
		}
	}
	return false
}

func cmdIncr(args []string) string {
	if len(args) != 2 {
		return encodeError("ERR wrong number of arguments for 'incr' command")
	}
	n, err := store.Incr(args[1])
	if err != nil {
		return encodeError(err.Error())
	}
	return encodeInteger(n)
}
