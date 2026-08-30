package main

import (
	"sort"
	"strconv"
)

// SortedSet is a set of unique members each with a float64 score, ordered by
// (score asc, then member lexicographically).
type SortedSet struct {
	members map[string]float64
}

func newSortedSet() *SortedSet {
	return &SortedSet{members: make(map[string]float64)}
}

// order returns members in sorted order.
func (z *SortedSet) order() []string {
	ms := make([]string, 0, len(z.members))
	for m := range z.members {
		ms = append(ms, m)
	}
	sort.Slice(ms, func(i, j int) bool {
		si, sj := z.members[ms[i]], z.members[ms[j]]
		if si != sj {
			return si < sj
		}
		return ms[i] < ms[j]
	})
	return ms
}

func (z *SortedSet) rank(member string) (int, bool) {
	if _, ok := z.members[member]; !ok {
		return 0, false
	}
	for i, m := range z.order() {
		if m == member {
			return i, true
		}
	}
	return 0, false
}

func formatScore(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func (s *Store) getSortedSetLocked(key string) (*SortedSet, bool, error) {
	e, ok := s.getLive(key)
	if !ok {
		return nil, false, nil
	}
	z, ok := e.value.(*SortedSet)
	if !ok {
		return nil, false, errWrongType
	}
	return z, true, nil
}

func (s *Store) getOrCreateSortedSetLocked(key string) (*SortedSet, error) {
	z, ok, err := s.getSortedSetLocked(key)
	if err != nil {
		return nil, err
	}
	if !ok {
		z = newSortedSet()
		s.data[key] = entry{value: z}
	}
	return z, nil
}

func cmdZAdd(args []string) string {
	if len(args) < 4 || len(args)%2 != 0 {
		return encodeError("ERR wrong number of arguments for 'zadd' command")
	}
	key := args[1]

	store.Lock()
	defer store.Unlock()
	z, err := store.getOrCreateSortedSetLocked(key)
	if err != nil {
		return wrongTypeReply
	}

	added := 0
	for i := 2; i < len(args); i += 2 {
		score, err := strconv.ParseFloat(args[i], 64)
		if err != nil {
			return encodeError("ERR value is not a valid float")
		}
		member := args[i+1]
		if _, exists := z.members[member]; !exists {
			added++
		}
		z.members[member] = score
	}
	store.touch(key)
	return encodeInteger(int64(added))
}

func cmdZRank(args []string) string {
	if len(args) != 3 {
		return encodeError("ERR wrong number of arguments for 'zrank' command")
	}
	store.Lock()
	defer store.Unlock()
	z, ok, err := store.getSortedSetLocked(args[1])
	if err != nil {
		return wrongTypeReply
	}
	if !ok {
		return encodeNullBulkString()
	}
	r, ok := z.rank(args[2])
	if !ok {
		return encodeNullBulkString()
	}
	return encodeInteger(int64(r))
}

func cmdZRange(args []string) string {
	if len(args) != 4 {
		return encodeError("ERR wrong number of arguments for 'zrange' command")
	}
	start, err1 := strconv.Atoi(args[2])
	stop, err2 := strconv.Atoi(args[3])
	if err1 != nil || err2 != nil {
		return encodeError("ERR value is not an integer or out of range")
	}

	store.Lock()
	defer store.Unlock()
	z, ok, err := store.getSortedSetLocked(args[1])
	if err != nil {
		return wrongTypeReply
	}
	if !ok {
		return encodeBulkArray(nil)
	}
	order := z.order()
	n := len(order)
	if start < 0 {
		start += n
	}
	if stop < 0 {
		stop += n
	}
	if start < 0 {
		start = 0
	}
	if stop >= n {
		stop = n - 1
	}
	if start > stop || start >= n {
		return encodeBulkArray(nil)
	}
	return encodeBulkArray(order[start : stop+1])
}

func cmdZCard(args []string) string {
	if len(args) != 2 {
		return encodeError("ERR wrong number of arguments for 'zcard' command")
	}
	store.Lock()
	defer store.Unlock()
	z, ok, err := store.getSortedSetLocked(args[1])
	if err != nil {
		return wrongTypeReply
	}
	if !ok {
		return encodeInteger(0)
	}
	return encodeInteger(int64(len(z.members)))
}

func cmdZScore(args []string) string {
	if len(args) != 3 {
		return encodeError("ERR wrong number of arguments for 'zscore' command")
	}
	store.Lock()
	defer store.Unlock()
	z, ok, err := store.getSortedSetLocked(args[1])
	if err != nil {
		return wrongTypeReply
	}
	if !ok {
		return encodeNullBulkString()
	}
	score, ok := z.members[args[2]]
	if !ok {
		return encodeNullBulkString()
	}
	return encodeBulkString(formatScore(score))
}

func cmdZRem(args []string) string {
	if len(args) < 3 {
		return encodeError("ERR wrong number of arguments for 'zrem' command")
	}
	store.Lock()
	defer store.Unlock()
	z, ok, err := store.getSortedSetLocked(args[1])
	if err != nil {
		return wrongTypeReply
	}
	if !ok {
		return encodeInteger(0)
	}
	removed := 0
	for _, m := range args[2:] {
		if _, exists := z.members[m]; exists {
			delete(z.members, m)
			removed++
		}
	}
	if len(z.members) == 0 {
		delete(store.data, args[1])
	}
	store.touch(args[1])
	return encodeInteger(int64(removed))
}
