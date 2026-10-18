package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var (
	ErrDocExists   = errors.New("document already exists")
	ErrDocNotFound = errors.New("document not found")
)

// DocMetadata captures persistence metadata for a document.
type DocMetadata struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	OwnerID   string    `json:"owner_id"`
	Type      string    `json:"type"` // "grid" for now
	CreatedAt time.Time `json:"created_at"`
}

// DocStore defines persistence operations for documents.
type DocStore interface {
	Create(meta DocMetadata) error
	List(ownerID string) ([]DocMetadata, error)
	Get(id string) (DocMetadata, error)
	Delete(id string) error
}

// FileDocStore implements DocStore backed by the local filesystem at data/docs/{id}/meta.json.
// Concurrency-safe via sync.RWMutex.
type FileDocStore struct {
	mu      sync.RWMutex
	dataDir string
}

// NewFileDocStore initializes a new FileDocStore at dataDir.
func NewFileDocStore(dataDir string) *FileDocStore {
	return &FileDocStore{
		dataDir: dataDir,
	}
}

// Close flushes any state and implements io.Closer semantics.
func (s *FileDocStore) Close() error {
	return nil
}

func (s *FileDocStore) docDir(id string) string {
	return filepath.Join(s.dataDir, "docs", id)
}

func (s *FileDocStore) metaPath(id string) string {
	return filepath.Join(s.docDir(id), "meta.json")
}

// Create persists a document's metadata to data/docs/{id}/meta.json.
func (s *FileDocStore) Create(meta DocMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.docDir(meta.ID)
	metaFile := s.metaPath(meta.ID)

	if _, err := os.Stat(metaFile); err == nil {
		return ErrDocExists
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create document directory: %w", err)
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode document metadata: %w", err)
	}

	if err := os.WriteFile(metaFile, data, 0o644); err != nil {
		return fmt.Errorf("failed to write document metadata file: %w", err)
	}

	return nil
}

// Get retrieves metadata for the given document ID.
func (s *FileDocStore) Get(id string) (DocMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	metaFile := s.metaPath(id)
	data, err := os.ReadFile(metaFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DocMetadata{}, ErrDocNotFound
		}
		return DocMetadata{}, fmt.Errorf("failed to read metadata file: %w", err)
	}

	var meta DocMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return DocMetadata{}, fmt.Errorf("failed to parse metadata file: %w", err)
	}
	return meta, nil
}

// List returns all documents owned by ownerID, sorted by CreatedAt descending.
// If ownerID is empty, it returns all documents (used by readiness check).
func (s *FileDocStore) List(ownerID string) ([]DocMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	docsDir := filepath.Join(s.dataDir, "docs")
	entries, err := os.ReadDir(docsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []DocMetadata{}, nil
		}
		return nil, fmt.Errorf("failed to read docs directory: %w", err)
	}

	var results []DocMetadata
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		metaFile := filepath.Join(docsDir, id, "meta.json")
		data, err := os.ReadFile(metaFile)
		if err != nil {
			continue
		}
		var meta DocMetadata
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		if ownerID == "" || meta.OwnerID == ownerID {
			results = append(results, meta)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	if results == nil {
		results = []DocMetadata{}
	}
	return results, nil
}

// Delete removes metadata and snapshot files for the specified document ID.
func (s *FileDocStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	metaFile := s.metaPath(id)
	if _, err := os.Stat(metaFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrDocNotFound
		}
		return err
	}

	dir := s.docDir(id)
	return os.RemoveAll(dir)
}
