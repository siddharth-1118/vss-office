package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

func TestIdentityService_RegisterAndLoginHappyPath(t *testing.T) {
	store := NewMemoryUserStore()
	secret := []byte("test-secret-key-32-bytes-minimum")
	svc := NewServiceWithConfig(store, ServiceConfig{
		JWTSecret:  secret,
		BcryptCost: bcrypt.MinCost, // Fast tests
		TokenTTL:   time.Hour,
	})

	user, err := svc.Register("alice@example.com", "supersecret")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if user.Email != "alice@example.com" {
		t.Errorf("expected email alice@example.com, got %s", user.Email)
	}
	if user.ID == "" {
		t.Errorf("expected non-empty user ID")
	}

	token, loggedUser, err := svc.Login("alice@example.com", "supersecret")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if loggedUser.ID != user.ID {
		t.Errorf("expected user ID %s, got %s", user.ID, loggedUser.ID)
	}
	if token == "" {
		t.Fatalf("expected non-empty token")
	}

	verifiedUser, err := svc.Verify(token)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if verifiedUser.ID != user.ID {
		t.Errorf("expected verified user ID %s, got %s", user.ID, verifiedUser.ID)
	}
}

func TestIdentityService_LoginWrongPassword(t *testing.T) {
	store := NewMemoryUserStore()
	secret := []byte("test-secret-key-32-bytes-minimum")
	svc := NewServiceWithConfig(store, ServiceConfig{
		JWTSecret:  secret,
		BcryptCost: bcrypt.MinCost,
		TokenTTL:   time.Hour,
	})

	_, err := svc.Register("bob@example.com", "correctpassword")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	_, _, err = svc.Login("bob@example.com", "wrongpassword")
	if err == nil {
		t.Fatalf("expected error on login with wrong password, got nil")
	}
	if err != ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestIdentityService_VerifyExpiredToken(t *testing.T) {
	store := NewMemoryUserStore()
	secret := []byte("test-secret-key-32-bytes-minimum")
	// Token with negative TTL to simulate expired token
	svc := NewServiceWithConfig(store, ServiceConfig{
		JWTSecret:  secret,
		BcryptCost: bcrypt.MinCost,
		TokenTTL:   -1 * time.Minute,
	})

	_, err := svc.Register("carol@example.com", "carolpass")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	token, _, err := svc.Login("carol@example.com", "carolpass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	_, err = svc.Verify(token)
	if err == nil {
		t.Fatalf("expected error on expired token verification, got nil")
	}
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestIdentityService_VerifyTamperedToken(t *testing.T) {
	store := NewMemoryUserStore()
	secret := []byte("test-secret-key-32-bytes-minimum")
	svc := NewServiceWithConfig(store, ServiceConfig{
		JWTSecret:  secret,
		BcryptCost: bcrypt.MinCost,
		TokenTTL:   time.Hour,
	})

	_, err := svc.Register("dave@example.com", "davepass")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	token, _, err := svc.Login("dave@example.com", "davepass")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	// Tamper with the token signature
	tampered := token[:len(token)-4] + "xxxx"
	_, err = svc.Verify(tampered)
	if err == nil {
		t.Fatalf("expected error on tampered token, got nil")
	}

	// Token signed with different secret
	diffToken, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "some-id",
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte("wrong-secret-key-32-bytes-minimum"))

	_, err = svc.Verify(diffToken)
	if err == nil {
		t.Fatalf("expected error on wrong key token, got nil")
	}

	// Tampered payload
	parts := strings.Split(token, ".")
	if len(parts) == 3 {
		tamperedPayload := parts[0] + ".e30." + parts[2]
		_, err = svc.Verify(tamperedPayload)
		if err == nil {
			t.Fatalf("expected error on altered payload, got nil")
		}
	}
}
