package main

import (
	"net"
	"strings"
)

// Client holds per-connection state (transactions, etc.).
type Client struct {
	conn    net.Conn
	inMulti bool
	queue   [][]string
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
			return encodeSimpleString("OK")
		case "MULTI":
			return encodeError("ERR MULTI calls can not be nested")
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
	}

	return execCommand(args)
}

func (c *Client) execTransaction() string {
	c.inMulti = false
	queued := c.queue
	c.queue = nil

	parts := make([]string, len(queued))
	for i, args := range queued {
		parts[i] = execCommand(args)
	}
	return encodeArray(parts)
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
