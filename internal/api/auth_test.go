package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vss-suite/vss/server/internal/identity"
	"golang.org/x/crypto/bcrypt"
)

func newTestAuthServer(t *testing.T) (*Server, *identity.Service, identity.UserStore) {
	t.Helper()
	store := identity.NewMemoryUserStore()
	authSvc := identity.NewServiceWithConfig(store, identity.ServiceConfig{
		JWTSecret:  []byte("test-auth-secret-key-32-bytes-minimum!"),
		BcryptCost: bcrypt.MinCost,
		TokenTTL:   time.Hour,
	})
	srv := NewServerWithOptions(ServerOptions{
		Auth:    authSvc,
		DataDir: t.TempDir(),
	})
	return srv, authSvc, store
}

func TestAuth_Register_HappyPath(t *testing.T) {
	srv, _, store := newTestAuthServer(t)

	body := `{"email": "alice@test.local", "password": "password123"}`
	req := httptest.NewRequest("POST", "/auth/register", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	srv.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp registerResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.ID == "" {
		t.Fatalf("expected non-empty user_id in response")
	}
	if resp.Email != "alice@test.local" {
		t.Fatalf("expected email 'alice@test.local', got %q", resp.Email)
	}

	// Verify user is actually persisted in identity.UserStore
	userInStore, err := store.ByID(resp.ID)
	if err != nil {
		t.Fatalf("user not found in store by ID: %v", err)
	}
	if userInStore.Email != "alice@test.local" {
		t.Fatalf("expected stored email 'alice@test.local', got %q", userInStore.Email)
	}
}

func TestAuth_Register_DuplicateEmail(t *testing.T) {
	srv, _, _ := newTestAuthServer(t)

	body := `{"email": "bob@test.local", "password": "password123"}`
	req1 := httptest.NewRequest("POST", "/auth/register", bytes.NewBufferString(body))
	w1 := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first registration failed: %d", w1.Code)
	}

	// Register duplicate
	req2 := httptest.NewRequest("POST", "/auth/register", bytes.NewBufferString(body))
	w2 := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Fatalf("expected status 409 for duplicate email, got %d", w2.Code)
	}
}

func TestAuth_Login_HappyPath(t *testing.T) {
	srv, authSvc, _ := newTestAuthServer(t)

	// Register via service
	user, err := authSvc.Register("charlie@test.local", "secretpass")
	if err != nil {
		t.Fatalf("failed to register user: %v", err)
	}

	body := `{"email": "charlie@test.local", "password": "secretpass"}`
	req := httptest.NewRequest("POST", "/auth/login", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp loginResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode login response: %v", err)
	}

	if resp.Token == "" {
		t.Fatalf("expected non-empty JWT token")
	}
	if resp.User.ID != user.ID || resp.User.Email != user.Email {
		t.Fatalf("expected user %+v, got %+v", user, resp.User)
	}

	// Verify token parses with authSvc
	verifiedUser, err := authSvc.Verify(resp.Token)
	if err != nil {
		t.Fatalf("token failed verification: %v", err)
	}
	if verifiedUser.ID != user.ID {
		t.Fatalf("expected verified user ID %q, got %q", user.ID, verifiedUser.ID)
	}
}

func TestAuth_Login_WrongPassword(t *testing.T) {
	srv, authSvc, _ := newTestAuthServer(t)

	_, err := authSvc.Register("david@test.local", "correctpass")
	if err != nil {
		t.Fatalf("failed to register user: %v", err)
	}

	body := `{"email": "david@test.local", "password": "wrongpassword"}`
	req := httptest.NewRequest("POST", "/auth/login", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	var errResp errorResponse
	_ = json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.Error == "" {
		t.Fatalf("expected non-empty error message")
	}
}

func TestAuth_Login_UnknownEmail(t *testing.T) {
	srv, _, _ := newTestAuthServer(t)

	body := `{"email": "nonexistent@test.local", "password": "anypassword"}`
	req := httptest.NewRequest("POST", "/auth/login", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	var errResp errorResponse
	_ = json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.Error != "invalid email or password" {
		t.Fatalf("expected error 'invalid email or password', got %q", errResp.Error)
	}
}

func TestAuth_RequireAuth_NoHeader(t *testing.T) {
	_, authSvc, _ := newTestAuthServer(t)

	handler := requireAuth(authSvc, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuth_RequireAuth_MalformedToken(t *testing.T) {
	_, authSvc, _ := newTestAuthServer(t)

	handler := requireAuth(authSvc, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-jwt-token")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuth_RequireAuth_ValidToken(t *testing.T) {
	_, authSvc, _ := newTestAuthServer(t)

	user, err := authSvc.Register("eva@test.local", "mypassword")
	if err != nil {
		t.Fatalf("failed to register user: %v", err)
	}
	token, _, err := authSvc.Login("eva@test.local", "mypassword")
	if err != nil {
		t.Fatalf("failed to login: %v", err)
	}

	var capturedUser identity.User
	var capturedOk bool
	handler := requireAuth(authSvc, func(w http.ResponseWriter, r *http.Request) {
		capturedUser, capturedOk = userFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !capturedOk {
		t.Fatalf("expected userFromCtx to return ok=true")
	}
	if capturedUser.ID != user.ID || capturedUser.Email != user.Email {
		t.Fatalf("expected captured user %+v, got %+v", user, capturedUser)
	}
}
