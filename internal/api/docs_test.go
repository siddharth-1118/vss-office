package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vss-suite/vss/server/internal/identity"
	"golang.org/x/crypto/bcrypt"
)

// failingDocStore mocks a broken DocStore to test readiness failures.
type failingDocStore struct {
	err error
}

func (m *failingDocStore) Create(meta DocMetadata) error { return m.err }
func (m *failingDocStore) List(ownerID string) ([]DocMetadata, error) {
	return nil, m.err
}
func (m *failingDocStore) Get(id string) (DocMetadata, error) { return DocMetadata{}, m.err }
func (m *failingDocStore) Delete(id string) error             { return m.err }

func newTestDocServer(t *testing.T) (*Server, *identity.Service, string) {
	t.Helper()
	dir := t.TempDir()
	store := identity.NewMemoryUserStore()
	authSvc := identity.NewServiceWithConfig(store, identity.ServiceConfig{
		JWTSecret:  []byte("test-doc-secret-key-32-bytes-minimum!!"),
		BcryptCost: bcrypt.MinCost,
		TokenTTL:   time.Hour,
	})
	docStore := NewFileDocStore(dir)
	srv := NewServerWithOptions(ServerOptions{
		Auth:     authSvc,
		DocStore: docStore,
		DataDir:  dir,
	})
	return srv, authSvc, dir
}

func registerAndLogin(t *testing.T, srv *Server, email, password string) string {
	t.Helper()
	regBody := map[string]string{"email": email, "password": password}
	b, _ := json.Marshal(regBody)
	wReg := httptest.NewRecorder()
	srv.Mux.ServeHTTP(wReg, httptest.NewRequest("POST", "/auth/register", bytes.NewReader(b)))
	if wReg.Code != http.StatusCreated {
		t.Fatalf("setup registration failed for %s: %d", email, wReg.Code)
	}

	wLog := httptest.NewRecorder()
	srv.Mux.ServeHTTP(wLog, httptest.NewRequest("POST", "/auth/login", bytes.NewReader(b)))
	if wLog.Code != http.StatusOK {
		t.Fatalf("setup login failed for %s: %d", email, wLog.Code)
	}
	var resp loginResponse
	_ = json.NewDecoder(wLog.Body).Decode(&resp)
	return resp.Token
}

func TestDocStore_Persistence_SurvivesRestart(t *testing.T) {
	sameDir := t.TempDir()
	store1 := NewFileDocStore(sameDir)
	id := "test-doc-uuid-1"
	meta := DocMetadata{
		ID:        id,
		Name:      "Q4 Financials",
		OwnerID:   "user-123",
		Type:      "grid",
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	if err := store1.Create(meta); err != nil {
		t.Fatalf("failed to create doc in store1: %v", err)
	}
	_ = store1.Close()

	// Reopen with fresh FileDocStore instance on the same directory
	store2 := NewFileDocStore(sameDir)
	got, err := store2.Get(id)
	if err != nil {
		t.Fatalf("failed to get doc from store2: %v", err)
	}

	if got.ID != meta.ID || got.Name != meta.Name || got.OwnerID != meta.OwnerID || got.Type != meta.Type || !got.CreatedAt.Equal(meta.CreatedAt) {
		t.Fatalf("assert got == meta failed:\n  got:  %+v\n  want: %+v", got, meta)
	}

	// Verify missing doc returns ErrDocNotFound
	_, errMissing := store2.Get("nonexistent-doc-id")
	if errMissing == nil {
		t.Fatalf("expected error for nonexistent doc, got nil (collision with previous doc)")
	}
	if !errors.Is(errMissing, ErrDocNotFound) {
		t.Fatalf("expected ErrDocNotFound, got %v", errMissing)
	}
}

func TestDocs_CreateAndList_HappyPath(t *testing.T) {
	srv, _, dataDir := newTestDocServer(t)
	token := registerAndLogin(t, srv, "alice@test.local", "secretpass")

	// 1. Create document
	reqBody := `{"name": "Budget 2026", "type": "grid"}`
	req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body: %s)", w.Code, w.Body.String())
	}

	var created DocMetadata
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("failed to decode created doc: %v", err)
	}
	if created.ID == "" || created.Name != "Budget 2026" || created.Type != "grid" {
		t.Fatalf("unexpected created doc: %+v", created)
	}

	// Verify snapshot file was persisted to data/docs/{id}/snapshot.json
	snapPath := filepath.Join(dataDir, "docs", created.ID, "snapshot.json")
	if _, err := os.Stat(snapPath); err != nil {
		t.Fatalf("snapshot file missing at %s: %v", snapPath, err)
	}

	// 2. Doc appears in GET /api/docs for that user
	listReq := httptest.NewRequest("GET", "/api/docs", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	wList := httptest.NewRecorder()
	srv.Mux.ServeHTTP(wList, listReq)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wList.Code)
	}

	var docs []DocMetadata
	if err := json.NewDecoder(wList.Body).Decode(&docs); err != nil {
		t.Fatalf("failed to decode docs list: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	if docs[0].ID != created.ID {
		t.Fatalf("expected doc ID %q, got %q", created.ID, docs[0].ID)
	}
}

func TestDocs_Create_PresentRefused(t *testing.T) {
	srv, _, _ := newTestDocServer(t)
	token := registerAndLogin(t, srv, "alice@test.local", "secretpass")

	reqBody := `{"name": "Pitch Deck", "type": "present"}`
	req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for type='present', got %d", w.Code)
	}

	var errResp errorResponse
	_ = json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.Error == "" {
		t.Fatalf("expected clear error message for unsupported type")
	}
}

func TestDocs_List_NoToken_Unauthorized(t *testing.T) {
	srv, _, _ := newTestDocServer(t)

	req := httptest.NewRequest("GET", "/api/docs", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestDocs_List_UserIsolation(t *testing.T) {
	srv, _, _ := newTestDocServer(t)
	tokenA := registerAndLogin(t, srv, "userA@test.local", "secretpass")
	tokenB := registerAndLogin(t, srv, "userB@test.local", "secretpass")

	// User A creates a document
	reqBody := `{"name": "User A Sheet", "type": "grid"}`
	req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("User A create doc failed: %d", w.Code)
	}

	// User B lists documents — must NOT return User A's docs
	listReq := httptest.NewRequest("GET", "/api/docs", nil)
	listReq.Header.Set("Authorization", "Bearer "+tokenB)
	wList := httptest.NewRecorder()
	srv.Mux.ServeHTTP(wList, listReq)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wList.Code)
	}

	var docsB []DocMetadata
	_ = json.NewDecoder(wList.Body).Decode(&docsB)
	if len(docsB) != 0 {
		t.Fatalf("expected 0 docs for user B, got %d: %+v", len(docsB), docsB)
	}
}

func TestDocs_Get_DifferentUser_NotFound(t *testing.T) {
	srv, _, _ := newTestDocServer(t)
	tokenA := registerAndLogin(t, srv, "userA@test.local", "secretpass")
	tokenB := registerAndLogin(t, srv, "userB@test.local", "secretpass")

	// User A creates doc
	reqBody := `{"name": "Private Document", "type": "grid"}`
	req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	var created DocMetadata
	_ = json.NewDecoder(w.Body).Decode(&created)

	// User B attempts to GET User A's doc
	getReq := httptest.NewRequest("GET", "/api/docs/"+created.ID, nil)
	getReq.Header.Set("Authorization", "Bearer "+tokenB)
	wGet := httptest.NewRecorder()
	srv.Mux.ServeHTTP(wGet, getReq)

	if wGet.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (unexpected document returned)", wGet.Code)
	}
}

func TestDocs_Delete_AndSubsequentGet(t *testing.T) {
	srv, _, dataDir := newTestDocServer(t)
	token := registerAndLogin(t, srv, "owner@test.local", "secretpass")

	// Create doc
	reqBody := `{"name": "To Be Deleted", "type": "grid"}`
	req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)
	var created DocMetadata
	_ = json.NewDecoder(w.Body).Decode(&created)

	// Delete doc
	delReq := httptest.NewRequest("DELETE", "/api/docs/"+created.ID, nil)
	delReq.Header.Set("Authorization", "Bearer "+token)
	wDel := httptest.NewRecorder()
	srv.Mux.ServeHTTP(wDel, delReq)

	if wDel.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content, got %d", wDel.Code)
	}

	// Subsequent GET must return 404
	getReq := httptest.NewRequest("GET", "/api/docs/"+created.ID, nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	wGet := httptest.NewRecorder()
	srv.Mux.ServeHTTP(wGet, getReq)

	if wGet.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for deleted doc, got %d", wGet.Code)
	}

	// Verify disk directory was deleted
	docDir := filepath.Join(dataDir, "docs", created.ID)
	if _, err := os.Stat(docDir); err == nil {
		t.Fatalf("expected doc directory %s to be deleted", docDir)
	}
}

func TestDocs_Readyz_Healthy(t *testing.T) {
	srv, _, _ := newTestDocServer(t)

	req := httptest.NewRequest("GET", "/readyz", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ready" {
		t.Fatalf("expected status 'ready', got %q", resp["status"])
	}
}

func TestDocs_Readyz_StoreFailure(t *testing.T) {
	srv, _, _ := newTestDocServer(t)
	// Inject a failing DocStore
	srv.DocStore = &failingDocStore{err: errors.New("simulated disk I/O failure")}

	req := httptest.NewRequest("GET", "/readyz", nil)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}

	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "not_ready" {
		t.Fatalf("expected status 'not_ready', got %q", resp["status"])
	}
	if resp["error"] == "" {
		t.Fatalf("expected non-empty error string in response")
	}
}

func TestDocs_CreateWriter_HappyPath(t *testing.T) {
	srv, _, _ := newTestDocServer(t)
	token := registerAndLogin(t, srv, "writer-creator@test.local", "secretpass123")

	reqBody := `{"name": "Meeting Notes", "type": "writer"}`
	req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d (body: %s)", w.Code, w.Body.String())
	}

	var meta DocMetadata
	if err := json.NewDecoder(w.Body).Decode(&meta); err != nil {
		t.Fatalf("failed to decode metadata: %v", err)
	}

	if meta.Type != "writer" {
		t.Fatalf("expected type 'writer', got %q", meta.Type)
	}
	if meta.Name != "Meeting Notes" {
		t.Fatalf("expected name 'Meeting Notes', got %q", meta.Name)
	}
}

func TestDocs_WriterExport_ReturnsEmptySnapshot(t *testing.T) {
	srv, _, _ := newTestDocServer(t)
	token := registerAndLogin(t, srv, "writer-export@test.local", "secretpass123")

	reqBody := `{"name": "Blank Writer", "type": "writer"}`
	req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	var meta DocMetadata
	_ = json.NewDecoder(w.Body).Decode(&meta)

	// Call export
	exportReq := httptest.NewRequest("GET", "/api/docs/"+meta.ID+"/export", nil)
	wExp := httptest.NewRecorder()
	srv.Mux.ServeHTTP(wExp, exportReq)

	if wExp.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (body: %s)", wExp.Code, wExp.Body.String())
	}

	var snap map[string]any
	if err := json.Unmarshal(wExp.Body.Bytes(), &snap); err != nil {
		t.Fatalf("failed to parse snapshot JSON: %v", err)
	}

	if snap["doc_id"] != meta.ID {
		t.Fatalf("expected doc_id %q, got %v", meta.ID, snap["doc_id"])
	}
	chars, ok := snap["chars"].([]any)
	if !ok || len(chars) != 0 {
		t.Fatalf("expected empty chars array, got %v", snap["chars"])
	}
	tombstones, ok := snap["tombstones"].([]any)
	if !ok || len(tombstones) != 0 {
		t.Fatalf("expected empty tombstones array, got %v", snap["tombstones"])
	}
}

func TestDocs_WriterImport_LoadsSnapshot(t *testing.T) {
	srv, _, _ := newTestDocServer(t)
	token := registerAndLogin(t, srv, "writer-import@test.local", "secretpass123")

	// Create doc
	reqBody := `{"name": "Import Target", "type": "writer"}`
	req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Mux.ServeHTTP(w, req)

	var meta DocMetadata
	_ = json.NewDecoder(w.Body).Decode(&meta)

	// Import snapshot with text
	snapJSON := `{
		"doc_id": "` + meta.ID + `",
		"chars": [
			{"id": "c1", "pos": "a0", "value": "H"},
			{"id": "c2", "pos": "a1", "value": "i"}
		],
		"tombstones": []
	}`
	importReq := httptest.NewRequest("POST", "/api/docs/"+meta.ID+"/import", bytes.NewBufferString(snapJSON))
	wImp := httptest.NewRecorder()
	srv.Mux.ServeHTTP(wImp, importReq)

	if wImp.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on import, got %d (body: %s)", wImp.Code, wImp.Body.String())
	}

	// Verify room now has imported content
	room := srv.Hub.GetOrCreateRoom(meta.ID)
	if room.Writer == nil || room.Writer.String() != "Hi" {
		t.Fatalf("expected room writer text 'Hi', got %v", room.Writer)
	}
}
