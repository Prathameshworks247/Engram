package main

import (
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
	mu   sync.Mutex
	cond *sync.Cond
	data map[string]entry
}

func NewStore() *Store {
	s := &Store{data: make(map[string]entry)}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *Store) Lock()   { s.mu.Lock() }
func (s *Store) Unlock() { s.mu.Unlock() }

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
