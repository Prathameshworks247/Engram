package main

import (
	"strconv"
	"strings"
	"time"
)

func cmdRPush(args []string) string { return push(args, false) }
func cmdLPush(args []string) string { return push(args, true) }

func push(args []string, left bool) string {
	if len(args) < 3 {
		return encodeError("ERR wrong number of arguments for 'push' command")
	}
	key := args[1]
	store.Lock()
	defer store.Unlock()

	l, err := store.getOrCreateListLocked(key)
	if err != nil {
		return wrongTypeReply
	}
	for _, v := range args[2:] {
		if left {
			l.items = append([]string{v}, l.items...)
		} else {
			l.items = append(l.items, v)
		}
	}
	store.cond.Broadcast()
	return encodeInteger(int64(len(l.items)))
}

func cmdLRange(args []string) string {
	if len(args) != 4 {
		return encodeError("ERR wrong number of arguments for 'lrange' command")
	}
	start, err1 := strconv.Atoi(args[2])
	stop, err2 := strconv.Atoi(args[3])
	if err1 != nil || err2 != nil {
		return encodeError("ERR value is not an integer or out of range")
	}

	store.Lock()
	defer store.Unlock()
	l, ok, err := store.getListLocked(args[1])
	if err != nil {
		return wrongTypeReply
	}
	if !ok {
		return encodeBulkArray(nil)
	}
	n := len(l.items)
	start = normIndex(start, n)
	stop = normIndex(stop, n)
	if start < 0 {
		start = 0
	}
	if stop >= n {
		stop = n - 1
	}
	if start > stop || start >= n {
		return encodeBulkArray(nil)
	}
	return encodeBulkArray(l.items[start : stop+1])
}

func normIndex(i, n int) int {
	if i < 0 {
		i += n
	}
	return i
}

func cmdLLen(args []string) string {
	if len(args) != 2 {
		return encodeError("ERR wrong number of arguments for 'llen' command")
	}
	store.Lock()
	defer store.Unlock()
	l, ok, err := store.getListLocked(args[1])
	if err != nil {
		return wrongTypeReply
	}
	if !ok {
		return encodeInteger(0)
	}
	return encodeInteger(int64(len(l.items)))
}

func cmdLPop(args []string) string {
	if len(args) < 2 || len(args) > 3 {
		return encodeError("ERR wrong number of arguments for 'lpop' command")
	}
	count := 1
	explicitCount := false
	if len(args) == 3 {
		c, err := strconv.Atoi(args[2])
		if err != nil || c < 0 {
			return encodeError("ERR value is out of range, must be positive")
		}
		count = c
		explicitCount = true
	}

	store.Lock()
	defer store.Unlock()
	l, ok, err := store.getListLocked(args[1])
	if err != nil {
		return wrongTypeReply
	}
	if !ok || len(l.items) == 0 {
		if explicitCount {
			return encodeBulkArray(nil)
		}
		return encodeNullBulkString()
	}
	if count > len(l.items) {
		count = len(l.items)
	}
	popped := append([]string(nil), l.items[:count]...)
	l.items = l.items[count:]
	if len(l.items) == 0 {
		delete(store.data, args[1])
	}
	if explicitCount {
		return encodeBulkArray(popped)
	}
	return encodeBulkString(popped[0])
}

func cmdBLPop(args []string) string {
	if len(args) < 3 {
		return encodeError("ERR wrong number of arguments for 'blpop' command")
	}
	timeoutSec, err := strconv.ParseFloat(args[len(args)-1], 64)
	if err != nil || timeoutSec < 0 {
		return encodeError("ERR timeout is not a float or out of range")
	}
	keys := args[1 : len(args)-1]

	var deadline time.Time
	if timeoutSec > 0 {
		deadline = time.Now().Add(time.Duration(timeoutSec * float64(time.Second)))
	}

	for {
		store.Lock()
		for _, k := range keys {
			l, ok, err := store.getListLocked(k)
			if err != nil {
				store.Unlock()
				return wrongTypeReply
			}
			if ok && len(l.items) > 0 {
				v := l.items[0]
				l.items = l.items[1:]
				if len(l.items) == 0 {
					delete(store.data, k)
				}
				store.Unlock()
				return encodeBulkArray([]string{k, v})
			}
		}
		store.Unlock()

		if timeoutSec > 0 && time.Now().After(deadline) {
			return encodeNullArray()
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var _ = strings.ToUpper
