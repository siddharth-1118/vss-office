package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vss-suite/vss/server/internal/collab"
	"github.com/vss-suite/vss/server/internal/crdt"
	"github.com/vss-suite/vss/server/internal/identity"
	"github.com/vss-suite/vss/server/internal/sheet"
	"golang.org/x/crypto/bcrypt"
)

func TestServerBoots(t *testing.T) {
	// Settle runtime and record starting goroutine count
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	startGoroutines := runtime.NumGoroutine()

	// 1. Boot httptest.NewServer with real identity.Service (low bcrypt cost) and real UserStore
	store := identity.NewMemoryUserStore()
	authSvc := identity.NewServiceWithConfig(store, identity.ServiceConfig{
		JWTSecret:  []byte("smoke-test-secret-key-32-bytes-minimum!"),
		BcryptCost: bcrypt.MinCost, // Fast tests without compromising bcrypt hashing logic
		TokenTTL:   time.Hour,
	})

	hub := collab.NewHub(authSvc)
	srv := NewServer(hub, authSvc)
	ts := httptest.NewServer(srv.Mux)
	defer ts.Close()

	// 2. Register user "alice@test.local" / "password123" through real identity service; get JWT
	user, err := authSvc.Register("alice@test.local", "password123")
	if err != nil {
		t.Fatalf("failed to register user: %v", err)
	}
	token, loggedUser, err := authSvc.Login("alice@test.local", "password123")
	if err != nil {
		t.Fatalf("failed to login: %v", err)
	}
	if loggedUser.ID != user.ID {
		t.Fatalf("expected logged user ID %q, got %q", user.ID, loggedUser.ID)
	}

	// 3. Create a grid document via existing POST /api/docs/<doc_id>/import
	docID := "doc-smoke-test"
	emptyGrid := crdt.NewGrid(docID)
	emptyCanon, err := emptyGrid.ToCanonicalJSON()
	if err != nil {
		t.Fatalf("ToCanonicalJSON failed: %v", err)
	}

	importURL := ts.URL + "/api/docs/" + docID + "/import"
	importResp, err := http.Post(importURL, "application/json", bytes.NewReader(emptyCanon))
	if err != nil {
		t.Fatalf("POST /import failed: %v", err)
	}
	if importResp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 from /import, got %d", importResp.StatusCode)
	}
	_ = importResp.Body.Close()

	// 4. Verify unauthenticated WebSocket handshake is rejected with 4001 close code
	unauthURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?doc_id=" + docID
	unauthConn, unauthResp, unauthErr := websocket.DefaultDialer.Dial(unauthURL, nil)
	if unauthErr != nil {
		if unauthResp != nil && unauthResp.StatusCode == http.StatusNotFound {
			t.Fatalf("route /ws is not registered on server: 404 Not Found")
		}
		closeErr, ok := unauthErr.(*websocket.CloseError)
		if !ok || closeErr.Code != 4001 {
			t.Fatalf("handshake token check failed: expected socket closed at 4001 without token, got dial error: %v", unauthErr)
		}
	} else {
		unauthConn.SetReadDeadline(time.Now().Add(time.Second))
		_, _, readErr := unauthConn.ReadMessage()
		unauthConn.Close()
		closeErr, ok := readErr.(*websocket.CloseError)
		if !ok || closeErr.Code != 4001 {
			t.Fatalf("handshake token check failed: expected socket closed at 4001 without token, got: %v", readErr)
		}
	}

	// 5. Open real WebSocket passing the JWT
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?doc_id=" + docID + "&token=" + token
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			t.Fatalf("route /ws is not registered on server: 404 Not Found")
		}
		t.Fatalf("websocket dial to %s failed: %v", wsURL, err)
	}
	defer conn.Close()

	// Assert welcome frame contains alice's user_id
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, welcomeBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read welcome frame: %v", err)
	}
	var welcomeMsg collab.Message
	if err := json.Unmarshal(welcomeBytes, &welcomeMsg); err != nil {
		t.Fatalf("failed to unmarshal welcome frame: %v", err)
	}
	if welcomeMsg.Type != collab.MsgWelcome {
		t.Fatalf("expected welcome frame, got message type %q", welcomeMsg.Type)
	}
	if welcomeMsg.UserID != user.ID {
		t.Fatalf("welcome frame user_id mismatch: expected %q, got %q", user.ID, welcomeMsg.UserID)
	}

	// 6. Send one grid-set-cell op for A1=1 as real collab.Message
	setA1Op, err := crdt.NewOp(docID, user.ID, crdt.OpGridSetCell, crdt.GridSetCellPayload{
		Row: 0,
		Col: 0,
		Raw: "1",
	})
	if err != nil {
		t.Fatalf("failed to create A1 op: %v", err)
	}

	msgA1 := collab.Message{
		Type: collab.MsgOp,
		Op:   &setA1Op,
	}
	dataA1, _ := json.Marshal(msgA1)
	if err := conn.WriteMessage(websocket.TextMessage, dataA1); err != nil {
		t.Fatalf("failed to write A1 op: %v", err)
	}

	// Read values frame: assert A1=1, recalc.FormulaVersion == "sheet/1", recalc.StaleReads == 0
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, valBytes1, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read values frame for A1: %v", err)
	}
	var valMsg1 collab.Message
	if err := json.Unmarshal(valBytes1, &valMsg1); err != nil {
		t.Fatalf("failed to unmarshal values frame 1: %v", err)
	}
	if valMsg1.Type != collab.MsgValues {
		t.Fatalf("expected MsgValues, got %q", valMsg1.Type)
	}
	if len(valMsg1.Values) == 0 || valMsg1.Values[0].Value.Display != "1" {
		t.Fatalf("expected values frame containing A1=1, got %v", valMsg1.Values)
	}
	if valMsg1.Recalc == nil {
		t.Fatalf("values frame missing RecalcSummary")
	}
	if valMsg1.Recalc.FormulaVersion != "sheet/1" {
		t.Errorf("expected FormulaVersion 'sheet/1', got %q", valMsg1.Recalc.FormulaVersion)
	}
	if valMsg1.Recalc.StaleReads != 0 {
		t.Errorf("expected StaleReads == 0, got %d", valMsg1.Recalc.StaleReads)
	}

	// 7. Send one grid-set-cell op for B1 with formula "=A1+1"
	b1Formula := "=A1+1"
	setB1Op, err := crdt.NewOp(docID, user.ID, crdt.OpGridSetCell, crdt.GridSetCellPayload{
		Row:     0,
		Col:     1,
		Formula: &b1Formula,
	})
	if err != nil {
		t.Fatalf("failed to create B1 op: %v", err)
	}

	msgB1 := collab.Message{
		Type: collab.MsgOp,
		Op:   &setB1Op,
	}
	dataB1, _ := json.Marshal(msgB1)
	if err := conn.WriteMessage(websocket.TextMessage, dataB1); err != nil {
		t.Fatalf("failed to write B1 op: %v", err)
	}

	// Read values frame: assert B1=2, recalc.Evaluated >= 2, recalc.Full == false
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, valBytes2, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read values frame for B1: %v", err)
	}
	var valMsg2 collab.Message
	if err := json.Unmarshal(valBytes2, &valMsg2); err != nil {
		t.Fatalf("failed to unmarshal values frame 2: %v", err)
	}
	if valMsg2.Type != collab.MsgValues {
		t.Fatalf("expected MsgValues, got %q", valMsg2.Type)
	}
	if valMsg2.Recalc == nil {
		t.Fatalf("values frame 2 missing RecalcSummary")
	}
	if valMsg2.Recalc.Evaluated < 2 {
		t.Errorf("expected recalc.Evaluated >= 2, got %d", valMsg2.Recalc.Evaluated)
	}
	if valMsg2.Recalc.Full {
		t.Errorf("expected recalc.Full == false, got true")
	}

	var foundB1 bool
	for _, it := range valMsg2.Values {
		if it.Row == 0 && it.Col == 1 {
			foundB1 = true
			if it.Value.Number == nil || *it.Value.Number != 2 {
				t.Errorf("expected B1=2, got %v", it.Value)
			}
		}
	}
	if !foundB1 {
		t.Errorf("B1 value not found in values frame")
	}

	// 8. Confirm via GET /api/docs/<id>/export that B1=2 is returned
	exportURL := ts.URL + "/api/docs/" + docID + "/export"
	exportResp, err := http.Get(exportURL)
	if err != nil {
		t.Fatalf("GET /export failed: %v", err)
	}
	defer exportResp.Body.Close()
	if exportResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /export, got %d", exportResp.StatusCode)
	}
	exportBytes, err := io.ReadAll(exportResp.Body)
	if err != nil {
		t.Fatalf("failed to read export response body: %v", err)
	}
	exportedGrid, err := crdt.FromCanonicalJSON(exportBytes)
	if err != nil {
		t.Fatalf("failed to reconstruct grid from export: %v", err)
	}
	b1Cell, ok := exportedGrid.GetCell(sheet.CellAddr{Row: 0, Col: 1})
	if !ok {
		t.Fatalf("cell B1 not found in exported document")
	}
	if b1Cell.Number == nil || *b1Cell.Number != 2 {
		t.Fatalf("expected exported cell B1=2, got %v", b1Cell)
	}

	// 9. Verify graceful shutdown integrity check on entrypoint (cmd/vss-server/main.go)
	mainCandidates := []string{
		"../../cmd/vss-server/main.go",
		"cmd/vss-server/main.go",
	}
	var mainFound bool
	for _, path := range mainCandidates {
		absPath, err := filepath.Abs(path)
		if err == nil {
			content, err := os.ReadFile(absPath)
			if err == nil {
				mainFound = true
				if !bytes.Contains(content, []byte("Shutdown(")) {
					t.Fatalf("goroutine leak: graceful shutdown removed from cmd/vss-server/main.go")
				}
				break
			}
		}
	}
	if !mainFound {
		t.Logf("warning: cmd/vss-server/main.go not found at relative paths, skipping entrypoint source check")
	}

	// 10. Close client connection and server, then assert goroutine count
	_ = conn.Close()
	ts.Close()

	// Settle duration: allow background goroutines and network buffers to fully drain
	time.Sleep(200 * time.Millisecond)
	runtime.GC()

	endGoroutines := runtime.NumGoroutine()
	// Document the +2 tolerance: Go runtime background goroutines (such as timer heap management,
	// background GC workers, and test runner finalizers) may fluctuate by up to +2 goroutines.
	diff := endGoroutines - startGoroutines
	if diff > 2 {
		t.Fatalf("goroutine-leak assertion failed: started with %d, ended with %d (difference %d exceeds +2 tolerance)",
			startGoroutines, endGoroutines, diff)
	}
	t.Logf("ServerBoots completed successfully. Start goroutines: %d, End: %d (diff: %d)",
		startGoroutines, endGoroutines, diff)
}
