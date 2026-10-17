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
)

func TestHub_CollabAndValuesBroadcast(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWS(hub, w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?doc_id=doc-test&actor_id=user-1"
	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial client 1 failed: %v", err)
	}
	defer conn1.Close()

	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "?doc_id=doc-test&actor_id=user-2"
	conn2, _, err := websocket.DefaultDialer.Dial(wsURL2, nil)
	if err != nil {
		t.Fatalf("dial client 2 failed: %v", err)
	}
	defer conn2.Close()

	// Wait for connections to register
	time.Sleep(50 * time.Millisecond)

	// Client 1 sends an op setting A1 formula to "=10+20"
	formula := "=10+20"
	setOp, err := crdt.NewOp("doc-test", "user-1", crdt.OpGridSetCell, crdt.GridSetCellPayload{
		Row:     0,
		Col:     0,
		Formula: &formula,
	})
	if err != nil {
		t.Fatalf("NewOp failed: %v", err)
	}

	sendMsg := Message{
		Type: MsgOp,
		Op:   &setOp,
	}
	data, _ := json.Marshal(sendMsg)
	if err := conn1.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("conn1 write failed: %v", err)
	}

	// Read from client 2: should receive sequenced op and values message
	var receivedOp bool
	var receivedValues bool
	var computedVal float64

	readDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(readDeadline) && (!receivedOp || !receivedValues) {
		conn2.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		_, msgBytes, err := conn2.ReadMessage()
		if err != nil {
			break
		}

		var msg Message
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}

		if msg.Type == MsgOp && msg.Op != nil {
			receivedOp = true
			if msg.Seq != 1 {
				t.Errorf("expected op Seq=1, got %d", msg.Seq)
			}
		} else if msg.Type == MsgValues {
			receivedValues = true
			if len(msg.Values) == 0 {
				t.Fatalf("values message empty")
			}
			item := msg.Values[0]
			if item.Row != 0 || item.Col != 0 {
				t.Errorf("expected values for (0,0), got (%d, %d)", item.Row, item.Col)
			}
			if item.Value.Number == nil {
				t.Fatalf("expected number value, got %v", item.Value)
			}
			computedVal = *item.Value.Number
		}
	}

	if !receivedOp {
		t.Errorf("client 2 did not receive sequenced op")
	}
	if !receivedValues {
		t.Errorf("client 2 did not receive recalculated values message")
	}
	if computedVal != 30 {
		t.Errorf("expected computed value 30, got %v", computedVal)
	}

	// Test reconnect / sync_request with Client 3
	wsURL3 := "ws" + strings.TrimPrefix(server.URL, "http") + "?doc_id=doc-test&actor_id=user-3"
	conn3, _, err := websocket.DefaultDialer.Dial(wsURL3, nil)
	if err != nil {
		t.Fatalf("dial client 3 failed: %v", err)
	}
	defer conn3.Close()

	syncReq := Message{
		Type:    MsgSyncRequest,
		LastSeq: 0,
	}
	syncData, _ := json.Marshal(syncReq)
	if err := conn3.WriteMessage(websocket.TextMessage, syncData); err != nil {
		t.Fatalf("conn3 write sync request failed: %v", err)
	}

	conn3.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, syncRespBytes, err := conn3.ReadMessage()
	if err != nil {
		t.Fatalf("conn3 read sync response failed: %v", err)
	}

	var syncResp Message
	if err := json.Unmarshal(syncRespBytes, &syncResp); err != nil {
		t.Fatalf("unmarshal sync response failed: %v", err)
	}
	if syncResp.Type != MsgSyncResponse {
		t.Fatalf("expected type sync_response, got %s", syncResp.Type)
	}
	if syncResp.LastSeq != 1 || len(syncResp.Ops) != 1 {
		t.Errorf("expected 1 op in sync response, got LastSeq=%d, ops=%d", syncResp.LastSeq, len(syncResp.Ops))
	}
}

func TestHub_ConcurrentWriteSafety(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWS(hub, w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?doc_id=doc-concurrent&actor_id=user-1"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	room := hub.GetOrCreateRoom("doc-concurrent")
	time.Sleep(50 * time.Millisecond)

	// Send 50 concurrent messages to the room's broadcast to stress-test writePump serialization
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
