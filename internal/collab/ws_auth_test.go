package collab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vss-suite/vss/server/internal/identity"
	"golang.org/x/crypto/bcrypt"
)

func newTestAuthService(t *testing.T) (*identity.Service, []byte) {
	t.Helper()
	store := identity.NewMemoryUserStore()
	secret := []byte("ws-auth-test-secret-key-32-bytes")
	svc := identity.NewServiceWithConfig(store, identity.ServiceConfig{
		JWTSecret:  secret,
		BcryptCost: bcrypt.MinCost,
		TokenTTL:   time.Hour,
	})
	return svc, secret
}

func assertClosedWith4001(t *testing.T, conn *websocket.Conn, dialErr error, failureCause string) {
	t.Helper()
	if dialErr != nil {
		if closeErr, ok := dialErr.(*websocket.CloseError); ok && closeErr.Code == 4001 {
			return
		}
		t.Fatalf("%s: expected close code 4001, got dial error: %v", failureCause, dialErr)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatalf("%s: expected socket closed with 4001, but socket remained open", failureCause)
	}
	closeErr, ok := err.(*websocket.CloseError)
	if !ok || closeErr.Code != 4001 {
		t.Fatalf("%s: expected close code 4001, got: %v", failureCause, err)
	}
}

func TestWSAuth_NoTokenRejected4001(t *testing.T) {
	authSvc, _ := newTestAuthService(t)
	hub := NewHub(authSvc)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWS(hub, w, r)
	}))
	defer server.Close()

	// Connect without token
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?doc_id=doc-1"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	assertClosedWith4001(t, conn, err, "accepted without token")
}

func TestWSAuth_ExpiredTokenRejected4001(t *testing.T) {
	store := identity.NewMemoryUserStore()
	secret := []byte("ws-auth-test-secret-key-32-bytes")
	// Service configured with negative TTL to produce expired tokens
	expiredAuthSvc := identity.NewServiceWithConfig(store, identity.ServiceConfig{
		JWTSecret:  secret,
		BcryptCost: bcrypt.MinCost,
		TokenTTL:   -1 * time.Minute,
	})
	user, err := expiredAuthSvc.Register("expired@test.local", "password123")
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	expiredToken, _, err := expiredAuthSvc.Login(user.Email, "password123")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	hub := NewHub(expiredAuthSvc)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWS(hub, w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?doc_id=doc-1&token=" + expiredToken
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	assertClosedWith4001(t, conn, err, "accepted with expired token")
}

func TestWSAuth_ValidTokenAcceptedWelcomeFrame(t *testing.T) {
	authSvc, _ := newTestAuthService(t)
	user, err := authSvc.Register("alice@test.local", "password123")
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	token, _, err := authSvc.Login(user.Email, "password123")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	hub := NewHub(authSvc)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWS(hub, w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?doc_id=doc-1&token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read welcome frame: %v", err)
	}

	var welcomeMsg Message
	if err := json.Unmarshal(msgBytes, &welcomeMsg); err != nil {
		t.Fatalf("unmarshal welcome message: %v", err)
	}

	if welcomeMsg.Type != MsgWelcome {
		t.Fatalf("expected message type %q, got %q", MsgWelcome, welcomeMsg.Type)
	}

	if welcomeMsg.UserID != user.ID || welcomeMsg.ActorID != user.ID {
		t.Fatalf("wrong user in welcome: expected user ID %q, got UserID=%q, ActorID=%q",
			user.ID, welcomeMsg.UserID, welcomeMsg.ActorID)
	}
}
