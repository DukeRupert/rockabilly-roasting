package store

// SessionStore provides database access for sessions, reset tokens, and email verifications.
type SessionStore struct{}

// NewSessionStore creates a new SessionStore.
func NewSessionStore() *SessionStore {
	return &SessionStore{}
}
