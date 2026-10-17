package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/vss-suite/vss/server/internal/collab"
	"github.com/vss-suite/vss/server/internal/crdt"
	"github.com/vss-suite/vss/server/internal/sheet"
)

func TestAPI_Healthz(t *testing.T) {
	srv := NewServer(nil)
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()

	srv.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", resp["status"])
	}
}

func TestAPI_DocImportExport(t *testing.T) {
	srv := NewServer(nil)

	// Create canonical snapshot with 2 cells
	grid := crdt.NewGrid("doc-api-test")
	num := 42.0
	grid.SetCell(sheet.CellAddr{Row: 0, Col: 0}, sheet.CellValue{
		Kind:    "number",
		Display: "42",
		Number:  &num,
	})
	formula := "=A1*2"
	grid.SetCell(sheet.CellAddr{Row: 1, Col: 0}, sheet.CellValue{
		Kind:    "number",
		Display: "84",
		Formula: formula,
	})

	canonicalJSON, err := grid.ToCanonicalJSON()
	if err != nil {
		t.Fatalf("ToCanonicalJSON failed: %v", err)
	}

	// POST /api/docs/doc-api-test/import
	importReq := httptest.NewRequest("POST", "/api/docs/doc-api-test/import", bytes.NewReader(canonicalJSON))
	importW := httptest.NewRecorder()
	srv.Mux.ServeHTTP(importW, importReq)

	if importW.Code != http.StatusOK {
		t.Fatalf("import failed with status %d: %s", importW.Code, importW.Body.String())
	}

	// GET /api/docs/doc-api-test/export
	exportReq := httptest.NewRequest("GET", "/api/docs/doc-api-test/export", nil)
	exportW := httptest.NewRecorder()
	srv.Mux.ServeHTTP(exportW, exportReq)

	if exportW.Code != http.StatusOK {
		t.Fatalf("export failed with status %d: %s", exportW.Code, exportW.Body.String())
	}

	exportedJSON := exportW.Body.Bytes()
	reconstructed, err := crdt.FromCanonicalJSON(exportedJSON)
	if err != nil {
		t.Fatalf("FromCanonicalJSON failed: %v", err)
	}

	cv0, ok := reconstructed.GetCell(sheet.CellAddr{Row: 0, Col: 0})
	if !ok || cv0.Number == nil || *cv0.Number != 42 {
		t.Errorf("expected A1=42, got %v", cv0)
	}
	cv1, ok := reconstructed.GetCell(sheet.CellAddr{Row: 1, Col: 0})
	if !ok || cv1.Formula != "=A1*2" {
		t.Errorf("expected A2 formula '=A1*2', got %v", cv1)
	}
}

func TestAPI_WebSocketEndpoint(t *testing.T) {
	srv := NewServer(nil)
	ts := httptest.NewServer(srv.Mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?doc_id=ws-test&actor_id=user-ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer conn.Close()

	// Send ping message
	pingMsg := collab.Message{Type: collab.MsgPing}
	data, _ := json.Marshal(pingMsg)
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("failed to write ping: %v", err)
	}

	// Receive pong response
	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read pong: %v", err)
	}

	var resp collab.Message
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("failed to unmarshal pong: %v", err)
	}
	if resp.Type != collab.MsgPong {
		t.Errorf("expected type pong, got %s", resp.Type)
	}
}
