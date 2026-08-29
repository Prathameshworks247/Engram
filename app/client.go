package main

import (
	"net"
	"strings"
)

// Client holds per-connection state (transactions, watched keys, etc.).
type Client struct {
	conn     net.Conn
	inMulti  bool
	queue    [][]string
	watching map[string]uint64 // key -> store version at WATCH time
}

func (c *Client) dispatch(args []string) string {
	cmd := strings.ToUpper(args[0])

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

	return execCommand(args)
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
