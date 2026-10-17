package crdt

import (
	"bytes"
	"testing"

	"github.com/vss-suite/vss/server/internal/sheet"
)

func TestGrid_SetCellAndStorageInterface(t *testing.T) {
	grid := NewGrid("doc-1")

	// Verify grid implements sheet.CellStorage
	var _ sheet.CellStorage = grid

	// Test NewOp with typed struct payload and pointer to formula
	formula := "=SUM(1, 2)"
	op, err := NewOp("doc-1", "user-1", OpGridSetCell, GridSetCellPayload{
		Row:     0,
		Col:     0,
		Raw:     "10",
		Formula: &formula,
	})
	if err != nil {
		t.Fatalf("NewOp failed: %v", err)
	}

	feed, err := grid.Apply(op)
	if err != nil {
		t.Fatalf("grid.Apply failed: %v", err)
	}
	if feed == nil || len(feed.Cells) != 1 || feed.Cells[0] != (sheet.CellAddr{Row: 0, Col: 0}) {
		t.Fatalf("expected feed with cell {0,0}, got %v", feed)
	}

	cv, ok := grid.GetCell(sheet.CellAddr{Row: 0, Col: 0})
	if !ok {
		t.Fatalf("GetCell returned false")
	}
	if cv.Formula != "=SUM(1, 2)" {
		t.Errorf("expected formula '=SUM(1, 2)', got %q", cv.Formula)
	}
	if cv.Number == nil || *cv.Number != 10 {
		t.Errorf("expected number 10, got %v", cv.Number)
	}

	// Set cell directly via sheet.CellStorage interface
	grid.SetCell(sheet.CellAddr{Row: 1, Col: 1}, sheet.CellValue{
		Kind:    "text",
		Display: "manual",
	})
	cvManual, ok := grid.GetCell(sheet.CellAddr{Row: 1, Col: 1})
	if !ok || cvManual.Display != "manual" {
		t.Errorf("expected manual cell, got %v", cvManual)
	}
}

func TestGrid_InsertDeleteAxis(t *testing.T) {
	grid := NewGrid("doc-1")

	// Set A1 (0,0) and A2 (1,0)
	op1, _ := NewOp("doc-1", "user-1", OpGridSetCell, GridSetCellPayload{Row: 0, Col: 0, Raw: "A1"})
	op2, _ := NewOp("doc-1", "user-1", OpGridSetCell, GridSetCellPayload{Row: 1, Col: 0, Raw: "A2"})
	_, _ = grid.Apply(op1)
	_, _ = grid.Apply(op2)

	// Insert 1 row at index 1
	insertOp, _ := NewOp("doc-1", "user-1", OpGridInsertAxis, GridInsertAxisPayload{
		Axis:  "row",
		Index: 1,
		Count: 1,
	})
	feed, err := grid.Apply(insertOp)
	if err != nil {
		t.Fatalf("insert row failed: %v", err)
	}
	if feed == nil || !feed.AxesChanged {
		t.Errorf("expected AxesChanged=true, got %v", feed)
	}

	// A1 (0,0) remains at (0,0)
	cv0, ok := grid.GetCell(sheet.CellAddr{Row: 0, Col: 0})
	if !ok || cv0.Display != "A1" {
		t.Errorf("expected A1 at (0,0), got %v", cv0)
	}
	// A2 shifted from (1,0) to (2,0)
	cv2, ok := grid.GetCell(sheet.CellAddr{Row: 2, Col: 0})
	if !ok || cv2.Display != "A2" {
		t.Errorf("expected A2 at (2,0), got %v", cv2)
	}
	// Row 1 should be empty
	_, ok1 := grid.GetCell(sheet.CellAddr{Row: 1, Col: 0})
	if ok1 {
		t.Errorf("expected row 1 to be empty after insert")
	}

	// Delete 1 row at index 1
	deleteOp, _ := NewOp("doc-1", "user-1", OpGridDeleteAxis, GridDeleteAxisPayload{
		Axis:  "row",
		Index: 1,
		Count: 1,
	})
	feed, err = grid.Apply(deleteOp)
	if err != nil {
		t.Fatalf("delete row failed: %v", err)
	}
	if feed == nil || !feed.AxesChanged {
		t.Errorf("expected AxesChanged=true, got %v", feed)
	}

	// A2 should be back at (1,0)
	cv1, ok := grid.GetCell(sheet.CellAddr{Row: 1, Col: 0})
	if !ok || cv1.Display != "A2" {
		t.Errorf("expected A2 back at (1,0), got %v", cv1)
	}
}

func TestGrid_ConcurrentLWWConvergence(t *testing.T) {
	g1 := NewGrid("doc-1")
	g2 := NewGrid("doc-1")

	// opA timestamp 100
	opA, _ := NewOp("doc-1", "actor-a", OpGridSetCell, GridSetCellPayload{
		Row: 0, Col: 0, Raw: "Alpha",
	})
	opA.Timestamp = 100

	// opB timestamp 200 (later write)
	opB, _ := NewOp("doc-1", "actor-b", OpGridSetCell, GridSetCellPayload{
		Row: 0, Col: 0, Raw: "Beta",
	})
	opB.Timestamp = 200

	// g1 applies opA then opB
	_, _ = g1.Apply(opA)
	_, _ = g1.Apply(opB)

	// g2 applies opB then opA (out-of-order)
	_, _ = g2.Apply(opB)
	_, _ = g2.Apply(opA)

	cv1, _ := g1.GetCell(sheet.CellAddr{Row: 0, Col: 0})
	cv2, _ := g2.GetCell(sheet.CellAddr{Row: 0, Col: 0})

	if cv1.Display != "Beta" {
		t.Errorf("g1 expected 'Beta', got %q", cv1.Display)
	}
	if cv2.Display != "Beta" {
		t.Errorf("g2 expected 'Beta', got %q", cv2.Display)
	}
}

func TestGrid_CanonicalRoundTrip(t *testing.T) {
	grid := NewGrid("doc-roundtrip")

	formula := "=A1*2"
	op1, _ := NewOp("doc-roundtrip", "u1", OpGridSetCell, GridSetCellPayload{Row: 0, Col: 0, Raw: "42"})
	op2, _ := NewOp("doc-roundtrip", "u1", OpGridSetCell, GridSetCellPayload{Row: 1, Col: 0, Formula: &formula})
	_, _ = grid.Apply(op1)
	_, _ = grid.Apply(op2)

	json1, err := grid.ToCanonicalJSON()
	if err != nil {
		t.Fatalf("ToCanonicalJSON failed: %v", err)
	}

	reconstructed, err := FromCanonicalJSON(json1)
	if err != nil {
		t.Fatalf("FromCanonicalJSON failed: %v", err)
	}

	json2, err := reconstructed.ToCanonicalJSON()
	if err != nil {
		t.Fatalf("reconstructed ToCanonicalJSON failed: %v", err)
	}

	if !bytes.Equal(json1, json2) {
		t.Errorf("canonical JSON mismatch:\nOriginal:\n%s\nReconstructed:\n%s", string(json1), string(json2))
	}
}
