package writer

import (
	"testing"

	"github.com/vss-suite/vss/server/internal/crdt"
)

func TestDoc_BasicInsertAndString(t *testing.T) {
	doc := NewDoc("doc-test")

	// Insert "Hello"
	chars, err := doc.InsertText("", "", "Hello", "actor-1")
	if err != nil {
		t.Fatalf("InsertText failed: %v", err)
	}
	if len(chars) != 5 {
		t.Fatalf("expected 5 chars, got %d", len(chars))
	}
	if doc.Len() != 5 {
		t.Fatalf("expected Len() == 5, got %d", doc.Len())
	}
	if got := doc.String(); got != "Hello" {
		t.Fatalf("expected 'Hello', got %q", got)
	}

	// Insert " World" after 'o'
	lastPos := chars[len(chars)-1].Pos
	_, err = doc.InsertText(lastPos, "", " World", "actor-1")
	if err != nil {
		t.Fatalf("InsertText after failed: %v", err)
	}
	if got := doc.String(); got != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", got)
	}
}

func TestDoc_DeleteAndLen(t *testing.T) {
	doc := NewDoc("doc-delete")
	chars, err := doc.InsertText("", "", "ABCDE", "actor-1")
	if err != nil {
		t.Fatalf("InsertText failed: %v", err)
	}

	// Delete 'C' (index 2) by ID
	if err := doc.Delete(chars[2].ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if doc.Len() != 4 {
		t.Fatalf("expected Len() == 4, got %d", doc.Len())
	}
	if got := doc.String(); got != "ABDE" {
		t.Fatalf("expected 'ABDE', got %q", got)
	}

	// DeleteRange between B and D inclusive: deletes B, C (already deleted), D
	fromPos := chars[1].Pos
	toPos := chars[3].Pos
	if err := doc.DeleteRange(fromPos, toPos); err != nil {
		t.Fatalf("DeleteRange failed: %v", err)
	}
	if doc.Len() != 2 {
		t.Fatalf("expected Len() == 2, got %d", doc.Len())
	}
	if got := doc.String(); got != "AE" {
		t.Fatalf("expected 'AE', got %q", got)
	}
}

func TestDoc_SnapshotRoundtrip(t *testing.T) {
	doc := NewDoc("doc-snap")
	chars, err := doc.InsertText("", "", "VSS Writer", "author-1")
	if err != nil {
		t.Fatalf("InsertText failed: %v", err)
	}

	// Delete space
	if err := doc.Delete(chars[3].ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	snapJSON, err := doc.ToSnapshotJSON()
	if err != nil {
		t.Fatalf("ToSnapshotJSON failed: %v", err)
	}

	restored, err := FromSnapshotJSON(snapJSON)
	if err != nil {
		t.Fatalf("FromSnapshotJSON failed: %v", err)
	}

	if restored.DocID != doc.DocID {
		t.Fatalf("doc_id mismatch: %s vs %s", restored.DocID, doc.DocID)
	}
	if restored.String() != doc.String() {
		t.Fatalf("content mismatch: %q vs %q", restored.String(), doc.String())
	}
	if restored.Len() != doc.Len() {
		t.Fatalf("len mismatch: %d vs %d", restored.Len(), doc.Len())
	}
}

func TestDoc_ApplyOps(t *testing.T) {
	doc := NewDoc("doc-ops")

	// Apply OpWriterInsertText
	insOp, err := crdt.NewOp(doc.DocID, "alice", crdt.OpWriterInsertText, crdt.WriterInsertTextPayload{
		Text: "Hello",
	})
	if err != nil {
		t.Fatalf("NewOp insert failed: %v", err)
	}
	if err := doc.Apply(insOp); err != nil {
		t.Fatalf("Apply insert op failed: %v", err)
	}
	if got := doc.String(); got != "Hello" {
		t.Fatalf("expected 'Hello', got %q", got)
	}

	// Apply OpWriterDeleteRange by IDs
	delOp, err := crdt.NewOp(doc.DocID, "alice", crdt.OpWriterDeleteRange, crdt.WriterDeleteRangePayload{
		IDs: []string{doc.Chars[0].ID},
	})
	if err != nil {
		t.Fatalf("NewOp delete failed: %v", err)
	}
	if err := doc.Apply(delOp); err != nil {
		t.Fatalf("Apply delete op failed: %v", err)
	}
	if got := doc.String(); got != "ello" {
		t.Fatalf("expected 'ello', got %q", got)
	}

	// Unsupported op returns error
	badOp, _ := crdt.NewOp(doc.DocID, "alice", "unknown.op", map[string]string{})
	if err := doc.Apply(badOp); err == nil {
		t.Fatalf("expected error for unknown op type, got nil")
	}
}
