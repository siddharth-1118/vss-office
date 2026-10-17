package identity

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidToken       = errors.New("invalid or expired token")
)

// TokenVerifier defines the verification interface required by collaboration transports.
type TokenVerifier interface {
	Verify(tokenString string) (User, error)
	VerifyClaims(tokenString string) (*Claims, User, error)
}

// Claims represents the JWT claims payload for VSS authentication.
type Claims struct {
	Email  string   `json:"email"`
	DocIDs []string `json:"doc_ids,omitempty"`
	jwt.RegisteredClaims
}

// HasDocPermission returns true if the claims permit access to the given docID.
func (c *Claims) HasDocPermission(docID string) bool {
	if len(c.DocIDs) == 0 {
		return true // Default open access if unconstrained
	}
	for _, id := range c.DocIDs {
		if id == "*" || id == docID {
			return true
		}
	}
	return false
}

// ServiceConfig provides configuration options for identity Service.
type ServiceConfig struct {
	JWTSecret  []byte
	BcryptCost int
	TokenTTL   time.Duration
}

// Service handles user registration, authentication, and JWT token issuance/verification.
type Service struct {
	store      UserStore
	jwtSecret  []byte
	bcryptCost int
	tokenTTL   time.Duration
}

// NewService initializes a Service with default settings (bcrypt DefaultCost, 24h TTL).
func NewService(store UserStore, secret []byte) *Service {
	return NewServiceWithConfig(store, ServiceConfig{
		JWTSecret:  secret,
		BcryptCost: bcrypt.DefaultCost,
		TokenTTL:   24 * time.Hour,
	})
}

// NewServiceWithConfig initializes a Service with customized configuration.
func NewServiceWithConfig(store UserStore, cfg ServiceConfig) *Service {
	cost := cfg.BcryptCost
	if cost <= 0 {
		cost = bcrypt.DefaultCost
	}
	ttl := cfg.TokenTTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	return &Service{
		store:      store,
		jwtSecret:  cfg.JWTSecret,
		bcryptCost: cost,
		tokenTTL:   ttl,
	}
}

// Register creates a new user with bcrypt-hashed password.
func (s *Service) Register(email, password string) (User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return User{}, fmt.Errorf("failed to hash password: %w", err)
	}
	return s.store.Create(email, string(hash))
}

// Login verifies credentials and returns a signed HS256 JWT and User.
func (s *Service) Login(email, password string) (string, User, error) {
	user, err := s.store.ByEmail(email)
	if err != nil {
		return "", User{}, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", User{}, ErrInvalidCredentials
	}

	now := time.Now().UTC()
	claims := Claims{
		Email:  user.Email,
		DocIDs: []string{"default", "*"},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.tokenTTL)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", User{}, fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, user, nil
}

// Verify validates a JWT and retrieves the authenticated User.
func (s *Service) Verify(tokenString string) (User, error) {
	_, user, err := s.VerifyClaims(tokenString)
	return user, err
}

// VerifyClaims validates a JWT and returns both the claims and User.
func (s *Service) VerifyClaims(tokenString string) (*Claims, User, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.jwtSecret, nil
	})

	if err != nil || !token.Valid {
		return nil, User{}, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || claims.Subject == "" {
		return nil, User{}, ErrInvalidToken
	}

	user, err := s.store.ByID(claims.Subject)
	if err != nil {
		return nil, User{}, ErrInvalidToken
	}

	return claims, user, nil
}
