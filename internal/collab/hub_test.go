package collab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vss-suite/vss/server/internal/crdt"
	"github.com/vss-suite/vss/server/internal/identity"
	"golang.org/x/crypto/bcrypt"
)

func newHubTestAuth(t *testing.T) *identity.Service {
	t.Helper()
	store := identity.NewMemoryUserStore()
	return identity.NewServiceWithConfig(store, identity.ServiceConfig{
		JWTSecret:  []byte("hub-test-jwt-secret-key-32-bytes"),
		BcryptCost: bcrypt.MinCost,
		TokenTTL:   time.Hour,
	})
}

func connectAuthenticatedWS(t *testing.T, serverURL, docID string, authSvc *identity.Service, email, password string) (*websocket.Conn, identity.User) {
	t.Helper()
	user, err := authSvc.Register(email, password)
	if err != nil {
		t.Fatalf("register failed for %s: %v", email, err)
	}
	token, _, err := authSvc.Login(email, password)
	if err != nil {
		t.Fatalf("login failed for %s: %v", email, err)
	}

	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "?doc_id=" + docID + "&token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed for %s: %v", email, err)
	}

	// Read welcome frame
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		t.Fatalf("failed reading welcome frame for %s: %v", email, err)
	}
	var welcome Message
	if err := json.Unmarshal(msgBytes, &welcome); err != nil {
		conn.Close()
		t.Fatalf("unmarshal welcome frame failed: %v", err)
	}
	if welcome.Type != MsgWelcome || welcome.UserID != user.ID {
		conn.Close()
		t.Fatalf("invalid welcome frame: type=%s, user=%s (expected %s)", welcome.Type, welcome.UserID, user.ID)
	}

	return conn, user
}

func TestHub_CollabAndValuesBroadcast(t *testing.T) {
	authSvc := newHubTestAuth(t)
	fixedTime := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	hub := NewHubWithOptions(HubOptions{
		Auth:  authSvc,
		Clock: func() time.Time { return fixedTime },
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWS(hub, w, r)
	}))
	defer server.Close()

	docID := "doc-collab-test"
	aliceConn, aliceUser := connectAuthenticatedWS(t, server.URL, docID, authSvc, "alice@test.local", "alicepass")
	defer aliceConn.Close()

	bobConn, _ := connectAuthenticatedWS(t, server.URL, docID, authSvc, "bob@test.local", "bobpass")
	defer bobConn.Close()

	// --- STEP 1: Alice sends op A1=1 (raw literal) ---
	// Alice receives values frame with A1=1 (author does not get op frame).
	// Bob receives op frame then values frame with A1=1.
	setA1Op, err := crdt.NewOp(docID, aliceUser.ID, crdt.OpGridSetCell, crdt.GridSetCellPayload{
		Row: 0,
		Col: 0,
		Raw: "1",
	})
	if err != nil {
		t.Fatalf("create setA1Op failed: %v", err)
	}
	aliceMsg1 := Message{Type: MsgOp, Op: &setA1Op}
	data1, _ := json.Marshal(aliceMsg1)
	if err := aliceConn.WriteMessage(websocket.TextMessage, data1); err != nil {
		t.Fatalf("aliceConn write op 1 failed: %v", err)
	}

	// Alice reads values frame for A1=1
	aliceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, aliceBytes1, err := aliceConn.ReadMessage()
	if err != nil {
		t.Fatalf("alice read values 1 failed: %v", err)
	}
	var aliceValues1 Message
	if err := json.Unmarshal(aliceBytes1, &aliceValues1); err != nil {
		t.Fatalf("alice unmarshal values 1 failed: %v", err)
	}
	if aliceValues1.Type != MsgValues {
		t.Fatalf("alice expected MsgValues, got %s", aliceValues1.Type)
	}
	if len(aliceValues1.Values) == 0 || aliceValues1.Values[0].Value.Display != "1" {
		t.Fatalf("alice expected values A1=1, got %v", aliceValues1.Values)
	}

	// Bob reads op frame then values frame
	bobConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, bobBytesOp1, err := bobConn.ReadMessage()
	if err != nil {
		t.Fatalf("bob read op 1 failed: %v", err)
	}
	var bobOp1 Message
	if err := json.Unmarshal(bobBytesOp1, &bobOp1); err != nil {
		t.Fatalf("bob unmarshal op 1 failed: %v", err)
	}
	if bobOp1.Type != MsgOp || bobOp1.Seq != 1 {
		t.Fatalf("bob expected op frame with Seq=1, got type=%s, seq=%d", bobOp1.Type, bobOp1.Seq)
	}

	bobConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, bobBytesVal1, err := bobConn.ReadMessage()
	if err != nil {
		t.Fatalf("bob read values 1 failed: %v", err)
	}
	var bobVal1 Message
	if err := json.Unmarshal(bobBytesVal1, &bobVal1); err != nil {
		t.Fatalf("bob unmarshal values 1 failed: %v", err)
	}
	if bobVal1.Type != MsgValues || len(bobVal1.Values) == 0 || bobVal1.Values[0].Value.Display != "1" {
		t.Fatalf("bob expected values frame A1=1, got %v", bobVal1.Values)
	}

	// --- STEP 2: Alice sends B1=A1+1 ---
	// Both receive values with B1=2, recalc.Evaluated >= 2, recalc.FormulaVersion == "sheet/1", recalc.StaleReads == 0.
	b1Formula := "=A1+1"
	setB1Op, err := crdt.NewOp(docID, aliceUser.ID, crdt.OpGridSetCell, crdt.GridSetCellPayload{
		Row:     0,
		Col:     1,
		Formula: &b1Formula,
	})
	if err != nil {
		t.Fatalf("create setB1Op failed: %v", err)
	}
	aliceMsg2 := Message{Type: MsgOp, Op: &setB1Op}
	data2, _ := json.Marshal(aliceMsg2)
	if err := aliceConn.WriteMessage(websocket.TextMessage, data2); err != nil {
		t.Fatalf("alice write op 2 failed: %v", err)
	}

	// Alice receives values frame
	aliceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, aliceBytes2, err := aliceConn.ReadMessage()
	if err != nil {
		t.Fatalf("alice read values 2 failed: %v", err)
	}
	var aliceValues2 Message
	if err := json.Unmarshal(aliceBytes2, &aliceValues2); err != nil {
		t.Fatalf("alice unmarshal values 2 failed: %v", err)
	}
	if aliceValues2.Type != MsgValues {
		t.Fatalf("alice expected values frame, got %s", aliceValues2.Type)
	}
	if aliceValues2.Recalc == nil {
		t.Fatalf("alice values frame missing RecalcSummary")
	}
	if aliceValues2.Recalc.Evaluated < 2 {
		t.Errorf("expected recalc.Evaluated >= 2, got %d", aliceValues2.Recalc.Evaluated)
	}
	if aliceValues2.Recalc.FormulaVersion != "sheet/1" {
		t.Errorf("expected recalc.FormulaVersion == 'sheet/1', got %q", aliceValues2.Recalc.FormulaVersion)
	}
	if aliceValues2.Recalc.StaleReads != 0 {
		t.Errorf("expected recalc.StaleReads == 0, got %d", aliceValues2.Recalc.StaleReads)
	}
	if aliceValues2.Recalc.Full {
		t.Errorf("expected incremental recalc (Full=false), got Full=true")
	}

	// Verify computed value B1=2
	var foundB1 bool
	for _, it := range aliceValues2.Values {
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

	// Bob receives op 2 then values 2
	bobConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, bobBytesOp2, err := bobConn.ReadMessage()
	if err != nil {
		t.Fatalf("bob read op 2 failed: %v", err)
	}
	var bobOp2 Message
	_ = json.Unmarshal(bobBytesOp2, &bobOp2)
	if bobOp2.Type != MsgOp || bobOp2.Seq != 2 {
		t.Fatalf("bob expected op frame Seq=2, got type=%s, seq=%d", bobOp2.Type, bobOp2.Seq)
	}

	bobConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, bobBytesVal2, err := bobConn.ReadMessage()
	if err != nil {
		t.Fatalf("bob read values 2 failed: %v", err)
	}
	var bobVal2 Message
	_ = json.Unmarshal(bobBytesVal2, &bobVal2)
	if bobVal2.Type != MsgValues || bobVal2.Recalc == nil || bobVal2.Recalc.Evaluated < 2 {
		t.Fatalf("bob expected values frame with Evaluated >= 2, got %v", bobVal2.Recalc)
	}

	// --- STEP 3: Volatile formula R1=NOW() with injected clock ---
	// Recalc.VolatileCells >= 1 on every frame
	r1Formula := "=NOW()"
	setR1Op, err := crdt.NewOp(docID, aliceUser.ID, crdt.OpGridSetCell, crdt.GridSetCellPayload{
		Row:     0,
		Col:     2,
		Formula: &r1Formula,
	})
	if err != nil {
		t.Fatalf("create setR1Op failed: %v", err)
	}
	aliceMsg3 := Message{Type: MsgOp, Op: &setR1Op}
	data3, _ := json.Marshal(aliceMsg3)
	if err := aliceConn.WriteMessage(websocket.TextMessage, data3); err != nil {
		t.Fatalf("alice write op 3 failed: %v", err)
	}

	aliceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, aliceBytes3, err := aliceConn.ReadMessage()
	if err != nil {
		t.Fatalf("alice read values 3 failed: %v", err)
	}
	var aliceValues3 Message
	_ = json.Unmarshal(aliceBytes3, &aliceValues3)
	if aliceValues3.Recalc == nil || aliceValues3.Recalc.VolatileCells < 1 {
		t.Fatalf("expected Recalc.VolatileCells >= 1, got %v", aliceValues3.Recalc)
	}

	// --- STEP 4: Client 3 sync_request ---
	carolConn, _ := connectAuthenticatedWS(t, server.URL, docID, authSvc, "carol@test.local", "carolpass")
	defer carolConn.Close()

	syncReq := Message{Type: MsgSyncRequest, LastSeq: 0}
	syncData, _ := json.Marshal(syncReq)
	if err := carolConn.WriteMessage(websocket.TextMessage, syncData); err != nil {
		t.Fatalf("carol sync request failed: %v", err)
	}

	carolConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, syncRespBytes, err := carolConn.ReadMessage()
	if err != nil {
		t.Fatalf("carol read sync response failed: %v", err)
	}
	var syncResp Message
	_ = json.Unmarshal(syncRespBytes, &syncResp)
	if syncResp.Type != MsgSyncResponse || syncResp.LastSeq != 3 || len(syncResp.Ops) != 3 {
		t.Fatalf("unexpected sync response: LastSeq=%d, ops count=%d", syncResp.LastSeq, len(syncResp.Ops))
	}
}

func TestHub_ConcurrentWriteSafety(t *testing.T) {
	authSvc := newHubTestAuth(t)
	hub := NewHub(authSvc)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWS(hub, w, r)
	}))
	defer server.Close()

	docID := "doc-concurrent"
	conn, _ := connectAuthenticatedWS(t, server.URL, docID, authSvc, "worker@test.local", "workerpass")
	defer conn.Close()

	room := hub.GetOrCreateRoom(docID)
	time.Sleep(50 * time.Millisecond)

	// Send 50 concurrent messages to stress-test writePump serialization
	done := make(chan bool)
	for i := 0; i < 50; i++ {
		go func(idx int) {
			room.mu.Lock()
			room.broadcastLocked(Message{
				Type: MsgPong,
				Seq:  int64(idx),
			})
			room.mu.Unlock()
			done <- true
		}(i)
	}

	for i := 0; i < 50; i++ {
		<-done
	}

	// Read messages without frame corruption or panic
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	readCount := 0
	for readCount < 50 {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
		readCount++
	}
	if readCount < 50 {
		t.Errorf("expected 50 messages read without error, got %d", readCount)
	}
}
