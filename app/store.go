package main

import (
	"errors"
	"strconv"
	"sync"
	"time"
)

type entry struct {
	value    any // string | *List
	expireAt time.Time
}

// List is a simple doubly-endable list backed by a slice.
type List struct {
	items []string
}

type Store struct {
	mu       sync.Mutex
	cond     *sync.Cond
	data     map[string]entry
	versions map[string]uint64 // bumped on every mutation, for WATCH
}

func NewStore() *Store {
	s := &Store{
		data:     make(map[string]entry),
		versions: make(map[string]uint64),
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *Store) Lock()   { s.mu.Lock() }
func (s *Store) Unlock() { s.mu.Unlock() }

// touch records a modification of key. Caller must hold the lock.
func (s *Store) touch(key string) { s.versions[key]++ }

// Touch is the exported, lock-taking form.
func (s *Store) Touch(key string) {
	s.mu.Lock()
	s.versions[key]++
	s.mu.Unlock()
}

// VersionLocked returns the current mutation counter for key. Caller holds the lock.
func (s *Store) VersionLocked(key string) uint64 { return s.versions[key] }

// Version returns the current mutation counter for key.
func (s *Store) Version(key string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.versions[key]
}

// getLive returns the entry if present and not expired. Caller must hold the lock.
func (s *Store) getLive(key string) (entry, bool) {
	e, ok := s.data[key]
	if !ok {
		return entry{}, false
	}
	if !e.expireAt.IsZero() && time.Now().After(e.expireAt) {
		delete(s.data, key)
		return entry{}, false
	}
	return e, true
}

func (s *Store) Set(key, value string, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := entry{value: value}
	if ttl > 0 {
		e.expireAt = time.Now().Add(ttl)
	}
	s.data[key] = e
	s.touch(key)
}

// SetAbsolute stores a string value with an optional absolute expiry
// (expireAtMs == 0 means no expiry). Used when loading an RDB file.
func (s *Store) SetAbsolute(key, value string, expireAtMs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := entry{value: value}
	if expireAtMs > 0 {
		e.expireAt = time.UnixMilli(expireAtMs)
	}
	s.data[key] = e
	s.touch(key)
}

// Keys returns all live (non-expired) keys.
func (s *Store) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.data))
	for k := range s.data {
		if _, ok := s.getLive(k); ok {
			out = append(out, k)
		}
	}
	return out
}

func (s *Store) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.getLive(key)
	if !ok {
		return "", false
	}
	str, ok := e.value.(string)
	return str, ok
}

// getListLocked returns the *List for key, or nil. Caller must hold the lock.
// Incr increments the integer value at key by 1, creating it at 1 if absent.
func (s *Store) Incr(key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.getLive(key)
	if !ok {
		s.data[key] = entry{value: "1"}
		s.touch(key)
		return 1, nil
	}
	str, ok := e.value.(string)
	if !ok {
		return 0, errWrongType
	}
	n, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		return 0, errors.New("ERR value is not an integer or out of range")
	}
	n++
	e.value = strconv.FormatInt(n, 10)
	s.data[key] = e
	s.touch(key)
	return n, nil
}

func (s *Store) getListLocked(key string) (*List, bool, error) {
	e, ok := s.getLive(key)
	if !ok {
		return nil, false, nil
	}
	l, ok := e.value.(*List)
	if !ok {
		return nil, false, errWrongType
	}
	return l, true, nil
}

// getOrCreateListLocked returns the *List for key, creating it if missing.
func (s *Store) getOrCreateListLocked(key string) (*List, error) {
	l, ok, err := s.getListLocked(key)
	if err != nil {
		return nil, err
	}
	if !ok {
		l = &List{}
		s.data[key] = entry{value: l}
	}
	return l, nil
}

func (s *Store) getStreamLocked(key string) (*Stream, bool, error) {
	e, ok := s.getLive(key)
	if !ok {
		return nil, false, nil
	}
	st, ok := e.value.(*Stream)
	if !ok {
		return nil, false, errWrongType
	}
	return st, true, nil
}

func (s *Store) getOrCreateStreamLocked(key string) (*Stream, error) {
	st, ok, err := s.getStreamLocked(key)
	if err != nil {
		return nil, err
	}
	if !ok {
		st = &Stream{}
		s.data[key] = entry{value: st}
	}
	return st, nil
}

func (s *Store) TypeOf(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.getLive(key)
	if !ok {
		return "none"
	}
	switch e.value.(type) {
	case string:
		return "string"
	case *List:
		return "list"
	case *Stream:
		return "stream"
	default:
		return "none"
	}
}
