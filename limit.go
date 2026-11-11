package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"sync"
	"time"
)

// Limiter allows up to PerClient requests per client, and Total requests
// overall, in each Window.
//
// Clients are counted by an HMAC of their address, keyed with a random key
// made for each window. When the window ends, the counts and the key are
// thrown away together, so what's left can't be matched to an address, not
// even by hashing every address again.
type Limiter struct {
	PerClient int
	Total     int
	Window    time.Duration
	Now       func() time.Time

	mu      sync.Mutex
	start   time.Time
	key     []byte
	total   int
	clients map[[16]byte]int
}

func (l *Limiter) Allow(client string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.expire()
	id := l.id(client)
	if l.total >= l.Total || l.clients[id] >= l.PerClient {
		return false
	}
	l.total++
	l.clients[id]++
	return true
}

// Run throws away each window's counts and key when the window ends, even if
// no request comes in to do it, until ctx is done.
func (l *Limiter) Run(ctx context.Context) {
	tick := time.NewTicker(min(l.Window, time.Minute))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			l.mu.Lock()
			l.expire()
			l.mu.Unlock()
		}
	}
}

// expire starts a new window, with a new key, if the current one is over.
// l.mu must be held.
func (l *Limiter) expire() {
	now := l.Now()
	if l.clients != nil && now.Sub(l.start) < l.Window {
		return
	}
	clear(l.key)
	l.key = make([]byte, 32)
	rand.Read(l.key)
	l.start, l.total, l.clients = now, 0, map[[16]byte]int{}
}

func (l *Limiter) id(client string) (id [16]byte) {
	mac := hmac.New(sha256.New, l.key)
	mac.Write([]byte(client))
	copy(id[:], mac.Sum(nil))
	return id
}
