package main

import (
	"strconv"
	"strings"
	"time"
)

func encodeStreamEntry(e StreamEntry) string {
	return encodeArray([]string{
		encodeBulkString(e.ID.String()),
		encodeBulkArray(e.Fields),
	})
}

func encodeEntries(entries []StreamEntry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = encodeStreamEntry(e)
	}
	return encodeArray(parts)
}

func cmdXAdd(args []string) string {
	if len(args) < 5 || len(args)%2 != 1 {
		return encodeError("ERR wrong number of arguments for 'xadd' command")
	}
	key := args[1]
	idRaw := args[2]
	fields := append([]string(nil), args[3:]...)

	store.Lock()
	defer store.Unlock()

	st, existed, err := store.getStreamLocked(key)
	if err != nil {
		return wrongTypeReply
	}
	target := st
	if !existed {
		target = &Stream{}
	}

	id, err := target.parseIDForXAdd(idRaw, uint64(time.Now().UnixMilli()))
	if err != nil {
		return encodeError(err.Error())
	}
	target.add(id, fields)
	if !existed {
		store.data[key] = entry{value: target}
	}
	store.cond.Broadcast()
	return encodeBulkString(id.String())
}

func cmdXRange(args []string) string {
	if len(args) != 4 {
		return encodeError("ERR wrong number of arguments for 'xrange' command")
	}
	start, err := parseRangeID(args[2], true)
	if err != nil {
		return encodeError(err.Error())
	}
	end, err := parseRangeID(args[3], false)
	if err != nil {
		return encodeError(err.Error())
	}

	store.Lock()
	defer store.Unlock()
	st, ok, err := store.getStreamLocked(args[1])
	if err != nil {
		return wrongTypeReply
	}
	if !ok {
		return encodeArray(nil)
	}
	return encodeEntries(st.rangeEntries(start, end))
}

func cmdXRead(args []string) string {
	blockMs := int64(-1)
	i := 1
	for i < len(args) {
		switch strings.ToUpper(args[i]) {
		case "BLOCK":
			if i+1 >= len(args) {
				return encodeError("ERR syntax error")
			}
			ms, err := strconv.ParseInt(args[i+1], 10, 64)
			if err != nil {
				return encodeError("ERR timeout is not an integer or out of range")
			}
			blockMs = ms
			i += 2
		case "COUNT":
			i += 2
		case "STREAMS":
			i++
			goto streams
		default:
			return encodeError("ERR syntax error")
		}
	}
streams:
	rest := args[i:]
	if len(rest) == 0 || len(rest)%2 != 0 {
		return encodeError("ERR Unbalanced XREAD list of streams: for each stream key an ID or '$' must be specified.")
	}
	n := len(rest) / 2
	keys := rest[:n]
	idRaws := rest[n:]

	afters := make([]StreamID, n)
	store.Lock()
	for j := 0; j < n; j++ {
		if idRaws[j] == "$" {
			st, ok, err := store.getStreamLocked(keys[j])
			if err != nil {
				store.Unlock()
				return wrongTypeReply
			}
			if ok {
				afters[j] = st.lastID
			}
		} else {
			id, err := parseReadID(idRaws[j])
			if err != nil {
				store.Unlock()
				return encodeError(err.Error())
			}
			afters[j] = id
		}
	}
	store.Unlock()

	var deadline time.Time
	if blockMs > 0 {
		deadline = time.Now().Add(time.Duration(blockMs) * time.Millisecond)
	}

	for {
		store.Lock()
		var results []string
		for j := 0; j < n; j++ {
			st, ok, err := store.getStreamLocked(keys[j])
			if err != nil {
				store.Unlock()
				return wrongTypeReply
			}
			if !ok {
				continue
			}
			es := st.entriesAfter(afters[j])
			if len(es) > 0 {
				results = append(results, encodeArray([]string{
					encodeBulkString(keys[j]),
					encodeEntries(es),
				}))
			}
		}
		store.Unlock()

		if len(results) > 0 {
			return encodeArray(results)
		}
		if blockMs < 0 {
			return encodeNullArray()
		}
		if blockMs > 0 && time.Now().After(deadline) {
			return encodeNullArray()
		}
		time.Sleep(5 * time.Millisecond)
	}
}
