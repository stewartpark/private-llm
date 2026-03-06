package main

import (
	"encoding/json"
	"hash/fnv"
	"sync"
)

// backendAssigner provides prompt cache-aware backend affinity for multi-instance
// Ollama. It maps conversation sessions to backends so the same conversation always
// hits the same Ollama instance, preserving KV cache across requests.
//
// Sessions are identified by fingerprinting the first 3 messages of a conversation
// (system + user + assistant). This uniquely identifies a session even when multiple
// tool instances share the same system prompt.
//
// Assignments use LRU eviction with a small capacity (2× numInstances) so stale
// sessions free their backends for new conversations.
type backendAssigner struct {
	mu           sync.Mutex
	numInstances int
	order        []uint64       // LRU order: oldest first
	assigned     map[uint64]int // fingerprint → backend (1-based)
	counts       [4]int         // assigned session count per backend
	inflight     [4]int         // in-flight request count per backend
	cap          int            // max entries (2 × numInstances)
}

func newBackendAssigner(numInstances int) *backendAssigner {
	return &backendAssigner{
		numInstances: numInstances,
		assigned:     make(map[uint64]int),
		cap:          max(2, numInstances*2),
	}
}

// Acquire returns the backend number (1-based) for the given request body and
// increments the in-flight counter for that backend. The caller must call
// Release when the request completes.
// Same conversation → same backend (KV cache preserved).
// Short conversations (< 3 messages) → least-loaded backend (no affinity).
func (a *backendAssigner) Acquire(body []byte) int {
	return a.pick(sessionFingerprint(body))
}

// Release decrements the in-flight counter for the given backend.
func (a *backendAssigner) Release(backend int) {
	a.mu.Lock()
	a.inflight[backend-1]--
	a.mu.Unlock()
}

// pick selects a backend for the given fingerprint.
// fingerprint == 0: no affinity, pick backend with fewest sessions.
// fingerprint != 0: sticky — reuse existing, or assign to least-loaded.
func (a *backendAssigner) pick(fingerprint uint64) int {
	if a.numInstances <= 1 {
		return 1
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Sticky: reuse existing assignment if backend is idle, otherwise overflow.
	// When the assigned backend is busy, its KV cache will be evicted anyway,
	// so reassign to an idle backend to avoid queuing for no benefit.
	if fingerprint != 0 {
		if b, ok := a.assigned[fingerprint]; ok {
			if a.inflight[b-1] == 0 {
				a.touch(fingerprint)
				a.inflight[b-1]++
				return b
			}
			// Assigned backend busy — unassign and fall through to pick idle.
			a.counts[b-1]--
			delete(a.assigned, fingerprint)
			for i, v := range a.order {
				if v == fingerprint {
					a.order = append(a.order[:i], a.order[i+1:]...)
					break
				}
			}
		}
	}

	// Evict oldest if at capacity
	if fingerprint != 0 && len(a.order) >= a.cap {
		a.evictOldest()
	}

	// Assign to backend with fewest sessions, tiebreak by in-flight count
	minIdx := 0
	for i := 1; i < a.numInstances; i++ {
		if a.counts[i] < a.counts[minIdx] {
			minIdx = i
		} else if a.counts[i] == a.counts[minIdx] && a.inflight[i] < a.inflight[minIdx] {
			minIdx = i
		}
	}
	backend := minIdx + 1

	if fingerprint != 0 {
		a.assigned[fingerprint] = backend
		a.counts[minIdx]++
		a.order = append(a.order, fingerprint)
	}
	a.inflight[minIdx]++
	return backend
}

// touch moves fingerprint to back of LRU (most recently used).
func (a *backendAssigner) touch(fp uint64) {
	for i, v := range a.order {
		if v == fp {
			a.order = append(a.order[:i], a.order[i+1:]...)
			a.order = append(a.order, fp)
			return
		}
	}
}

// evictOldest removes the least recently used session and decrements its backend count.
func (a *backendAssigner) evictOldest() {
	if len(a.order) == 0 {
		return
	}
	oldest := a.order[0]
	a.order = a.order[1:]
	if b, ok := a.assigned[oldest]; ok {
		a.counts[b-1]--
		delete(a.assigned, oldest)
	}
}

// sessionFingerprint extracts a stable session identifier from a request body.
// Hashes the first 3 messages of the conversation (role + content) via FNV-1a.
// Returns 0 for short conversations (< 3 messages) where prefill cost is negligible.
func sessionFingerprint(body []byte) uint64 {
	var peek struct {
		Messages []chatMessage `json:"messages"`
	}
	if json.Unmarshal(body, &peek) != nil {
		return 0
	}

	if len(peek.Messages) < 3 {
		return 0
	}

	h := fnv.New64a()
	for i := range 3 {
		_, _ = h.Write([]byte(peek.Messages[i].Role))
		_, _ = h.Write(peek.Messages[i].Content)
	}
	return h.Sum64()
}
