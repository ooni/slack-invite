package main

import (
	"sync"
	"time"
)

// Limiter allows up to PerClient requests per client, and Total requests
// overall, in each Window. It keeps only counters, for the current window.
type Limiter struct {
	PerClient int
	Total     int
	Window    time.Duration
	Now       func() time.Time

	mu      sync.Mutex
	start   time.Time
	total   int
	clients map[string]int
}

func (l *Limiter) Allow(client string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.Now()
	if l.clients == nil || now.Sub(l.start) >= l.Window {
		l.start, l.total, l.clients = now, 0, map[string]int{}
	}
	if l.total >= l.Total || l.clients[client] >= l.PerClient {
		return false
	}
	l.total++
	l.clients[client]++
	return true
}
