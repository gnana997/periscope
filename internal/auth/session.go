package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// Session is the server-side session record. Tokens never leave this
// package — handlers downstream see the public Identity slice.
type Session struct {
	ID             string
	Subject        string
	Email          string
	Groups         []string
	AccessToken    string
	RefreshToken   string
	IDToken        string
	AccessExpiry   time.Time
	AbsoluteExpiry time.Time
	LastActivity   time.Time
}

// Identity is the public face of a session, safe to surface to
// handlers and (via /auth/whoami) the SPA.
type Identity struct {
	Subject string   `json:"subject"`
	Email   string   `json:"email"`
	Groups  []string `json:"groups"`
}

// ToIdentity returns the Session's public projection.
func (s Session) ToIdentity() Identity {
	return Identity{
		Subject: s.Subject,
		Email:   s.Email,
		Groups:  append([]string(nil), s.Groups...),
	}
}

// SessionStore is the interface every backing store satisfies.
type SessionStore interface {
	Create(s Session) error
	Get(id string) (Session, bool)
	Update(s Session) error
	Delete(id string) error
}

// MemoryStore is the v1 implementation: a sync.Map of session records
// with periodic cleanup of expired entries. Lost on restart, which is
// acceptable for single-replica v1.
type MemoryStore struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

// NewMemoryStore returns an empty MemoryStore. Call Run once with a
// derived context to start the background cleanup goroutine.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: map[string]Session{}}
}

// Run starts the background cleanup goroutine. It exits when ctx is
// cancelled. Safe to call once at startup.
func (m *MemoryStore) Run(ctx context.Context) {
	t := time.NewTicker(1 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.cleanup()
		}
	}
}

func (m *MemoryStore) cleanup() {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		if now.After(s.AbsoluteExpiry) {
			delete(m.sessions, id)
		}
	}
}

func (m *MemoryStore) Create(s Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.ID] = s
	return nil
}

func (m *MemoryStore) Get(id string) (Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *MemoryStore) Update(s Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[s.ID]; !ok {
		return ErrNotFound
	}
	m.sessions[s.ID] = s
	return nil
}

func (m *MemoryStore) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
	return nil
}

// ErrNotFound signals a missing session in Update/Delete.
var ErrNotFound = errNotFound{}

type errNotFound struct{}

func (errNotFound) Error() string { return "session not found" }

// NewSessionID returns a 256-bit random session identifier encoded as
// URL-safe base64 (no padding).
func NewSessionID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should not fail in practice; if it does, the
		// process is in no shape to issue sessions.
		panic("auth: rand.Read failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
