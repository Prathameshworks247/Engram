package main

import (
	"sync"
	"time"
)

type entry struct {
	value    string
	expireAt time.Time // zero means no expiry
}

type Store struct {
	mu   sync.Mutex
	data map[string]entry
}

func NewStore() *Store {
	return &Store{data: make(map[string]entry)}
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
	e, ok := s.data[key]
	if !ok {
		return "", false
	}
	if !e.expireAt.IsZero() && time.Now().After(e.expireAt) {
		delete(s.data, key)
		return "", false
	}
	return e.value, true
}
