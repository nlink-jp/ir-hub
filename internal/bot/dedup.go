package bot

import (
	"sync"
	"time"
)

// dedupTTL is how long an event key is remembered. Slack retries
// undelivered events for minutes, not hours.
const dedupTTL = 5 * time.Minute

// dedupSweepThreshold caps memory: when the set grows past this,
// expired entries are swept on insert.
const dedupSweepThreshold = 4096

// dedup is an in-memory TTL set for event/envelope IDs. Redelivered
// Socket Mode envelopes (after reconnects or missed acks) are
// dropped here; message-level duplicates are additionally absorbed
// by the store's primary key.
type dedup struct {
	mu   sync.Mutex
	seen map[string]time.Time // key → expiry
	now  func() time.Time
}

func newDedup(now func() time.Time) *dedup {
	return &dedup{seen: map[string]time.Time{}, now: now}
}

// Seen reports whether key was already recorded (and not expired),
// recording it as a side effect.
func (d *dedup) Seen(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	if exp, ok := d.seen[key]; ok && now.Before(exp) {
		return true
	}
	if len(d.seen) >= dedupSweepThreshold {
		for k, exp := range d.seen {
			if !now.Before(exp) {
				delete(d.seen, k)
			}
		}
	}
	d.seen[key] = now.Add(dedupTTL)
	return false
}
