package identity

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUserExists   = errors.New("user already exists")
	ErrUserNotFound = errors.New("user not found")
)

// UserStore provides persistence operations for users.
type UserStore interface {
	Create(email, hash string) (User, error)
	ByEmail(email string) (User, error)
	ByID(id string) (User, error)
}

// MemoryUserStore is an in-memory implementation of UserStore.
type MemoryUserStore struct {
	mu      sync.RWMutex
	byID    map[string]User
	byEmail map[string]string
}

// NewMemoryUserStore initializes a new MemoryUserStore.
func NewMemoryUserStore() *MemoryUserStore {
	return &MemoryUserStore{
		byID:    make(map[string]User),
		byEmail: make(map[string]string),
	}
}

func (s *MemoryUserStore) Create(email, hash string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	normalized := strings.ToLower(strings.TrimSpace(email))
	if _, exists := s.byEmail[normalized]; exists {
		return User{}, ErrUserExists
	}

	user := User{
		ID:           uuid.NewString(),
		Email:        normalized,
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	}

	s.byID[user.ID] = user
	s.byEmail[normalized] = user.ID
	return user, nil
}

func (s *MemoryUserStore) ByEmail(email string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	normalized := strings.ToLower(strings.TrimSpace(email))
	id, ok := s.byEmail[normalized]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return s.byID[id], nil
}

func (s *MemoryUserStore) ByID(id string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.byID[id]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return user, nil
}
