package main

import (
	"strings"
	"sync"
)

// pubsub is the global channel -> subscribers registry.
var pubsub = struct {
	mu       sync.Mutex
	channels map[string]map[*Client]bool
}{channels: make(map[string]map[*Client]bool)}

func (c *Client) cmdSubscribe(args []string) string {
	if len(args) < 2 {
		return encodeError("ERR wrong number of arguments for 'subscribe' command")
	}
	if c.subs == nil {
		c.subs = make(map[string]bool)
	}
	var b strings.Builder
	for _, ch := range args[1:] {
		pubsub.mu.Lock()
		if pubsub.channels[ch] == nil {
			pubsub.channels[ch] = make(map[*Client]bool)
		}
		pubsub.channels[ch][c] = true
		pubsub.mu.Unlock()
		c.subs[ch] = true

		b.WriteString(encodeArray([]string{
			encodeBulkString("subscribe"),
			encodeBulkString(ch),
			encodeInteger(int64(len(c.subs))),
		}))
	}
	return b.String()
}

func (c *Client) cmdUnsubscribe(args []string) string {
	targets := args[1:]
	if len(targets) == 0 {
		for ch := range c.subs {
			targets = append(targets, ch)
		}
	}
	if len(targets) == 0 {
		return encodeArray([]string{
			encodeBulkString("unsubscribe"),
			encodeNullBulkString(),
			encodeInteger(0),
		})
	}

	var b strings.Builder
	for _, ch := range targets {
		pubsub.mu.Lock()
		if subs := pubsub.channels[ch]; subs != nil {
			delete(subs, c)
			if len(subs) == 0 {
				delete(pubsub.channels, ch)
			}
		}
		pubsub.mu.Unlock()
		delete(c.subs, ch)

		b.WriteString(encodeArray([]string{
			encodeBulkString("unsubscribe"),
			encodeBulkString(ch),
			encodeInteger(int64(len(c.subs))),
		}))
	}
	return b.String()
}

func cmdPublish(args []string) string {
	if len(args) != 3 {
		return encodeError("ERR wrong number of arguments for 'publish' command")
	}
	channel, payload := args[1], args[2]

	pubsub.mu.Lock()
	subs := make([]*Client, 0, len(pubsub.channels[channel]))
	for cl := range pubsub.channels[channel] {
		subs = append(subs, cl)
	}
	pubsub.mu.Unlock()

	msg := []byte(encodeArray([]string{
		encodeBulkString("message"),
		encodeBulkString(channel),
		encodeBulkString(payload),
	}))
	for _, cl := range subs {
		cl.send(msg)
	}
	return encodeInteger(int64(len(subs)))
}

// removeFromPubSub drops the client from every channel it was subscribed to.
func (c *Client) removeFromPubSub() {
	if len(c.subs) == 0 {
		return
	}
	pubsub.mu.Lock()
	for ch := range c.subs {
		if subs := pubsub.channels[ch]; subs != nil {
			delete(subs, c)
			if len(subs) == 0 {
				delete(pubsub.channels, ch)
			}
		}
	}
	pubsub.mu.Unlock()
	c.subs = nil
}

// allowedInSubscribeMode reports whether cmd may run while the connection is
// in subscribed mode (RESP2).
func allowedInSubscribeMode(cmd string) bool {
	switch cmd {
	case "SUBSCRIBE", "UNSUBSCRIBE", "PSUBSCRIBE", "PUNSUBSCRIBE",
		"SSUBSCRIBE", "SUNSUBSCRIBE", "PING", "QUIT", "RESET":
		return true
	}
	return false
}
