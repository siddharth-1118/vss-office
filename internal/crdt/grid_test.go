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

func TestGrid_FormulaRewritingOnAxisShift(t *testing.T) {
	grid := NewGrid("doc-shift")

	// Set A1=10, A2=20, B1="=A1+A2"
	formula := "=A1+A2"
	opA1, _ := NewOp("doc-shift", "u1", OpGridSetCell, GridSetCellPayload{Row: 0, Col: 0, Raw: "10"})
	opA2, _ := NewOp("doc-shift", "u1", OpGridSetCell, GridSetCellPayload{Row: 1, Col: 0, Raw: "20"})
	opB1, _ := NewOp("doc-shift", "u1", OpGridSetCell, GridSetCellPayload{Row: 0, Col: 1, Formula: &formula})

	_, _ = grid.Apply(opA1)
	_, _ = grid.Apply(opA2)
	_, _ = grid.Apply(opB1)

	// Insert row at index 1 count 1 (between row 0 and row 1)
	insertOp, _ := NewOp("doc-shift", "u1", OpGridInsertAxis, GridInsertAxisPayload{
		Axis:  "row",
		Index: 1,
		Count: 1,
	})
	_, err := grid.Apply(insertOp)
	if err != nil {
		t.Fatalf("insert axis failed: %v", err)
	}

	// B1 should have rewritten formula "=A1+A3" because A2 shifted to A3
	cvB1, ok := grid.GetCell(sheet.CellAddr{Row: 0, Col: 1})
	if !ok {
		t.Fatalf("B1 not found")
	}
	if cvB1.Formula != "=A1+A3" {
		t.Errorf("expected rewritten formula '=A1+A3', got %q", cvB1.Formula)
	}
}

func TestGrid_FormulaClearing(t *testing.T) {
	grid := NewGrid("doc-clear")

	// Set cell (0,0) with formula
	formula := "=1+1"
	op1, _ := NewOp("doc-clear", "u1", OpGridSetCell, GridSetCellPayload{
		Row:     0,
		Col:     0,
		Formula: &formula,
	})
	_, _ = grid.Apply(op1)

	cv, _ := grid.GetCell(sheet.CellAddr{Row: 0, Col: 0})
	if cv.Formula != "=1+1" {
		t.Fatalf("expected formula '=1+1', got %q", cv.Formula)
	}

	// Overwrite cell (0,0) with raw text and nil formula
	op2, _ := NewOp("doc-clear", "u1", OpGridSetCell, GridSetCellPayload{
		Row: 0,
		Col: 0,
		Raw: "99",
	})
	_, err := grid.Apply(op2)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	cv2, _ := grid.GetCell(sheet.CellAddr{Row: 0, Col: 0})
	if cv2.Formula != "" {
		t.Errorf("expected formula to be cleared, got %q", cv2.Formula)
	}
	if cv2.Number == nil || *cv2.Number != 99 {
		t.Errorf("expected raw number 99, got %v", cv2)
	}
}

func TestConcurrentRowInsertBothOrders(t *testing.T) {
	initial := []string{"r0", "r1", "r2"}
	gA := NewGrid("doc-row-both")
	gB := NewGrid("doc-row-both")

	for r, val := range initial {
		op, _ := NewOp("doc-row-both", "init", OpGridSetCell, GridSetCellPayload{Row: r, Col: 0, Raw: val})
		_, _ = gA.Apply(op)
		_, _ = gB.Apply(op)
	}

	opInsA, _ := NewOp("doc-row-both", "actor-A", OpGridInsertAxis, GridInsertAxisPayload{Axis: "row", Index: 1, Count: 1})
	opSetA, _ := NewOp("doc-row-both", "actor-A", OpGridSetCell, GridSetCellPayload{Row: 1, Col: 0, Raw: "A_row"})

	opInsB, _ := NewOp("doc-row-both", "actor-B", OpGridInsertAxis, GridInsertAxisPayload{Axis: "row", Index: 1, Count: 1})
	opSetB, _ := NewOp("doc-row-both", "actor-B", OpGridSetCell, GridSetCellPayload{Row: 1, Col: 0, Raw: "B_row"})

	// gA applies local A, then remote B
	_, _ = gA.Apply(opInsA)
	_, _ = gA.Apply(opSetA)
	_, _ = gA.Apply(opInsB)
	_, _ = gA.Apply(opSetB)

	// gB applies local B, then remote A
	_, _ = gB.Apply(opInsB)
	_, _ = gB.Apply(opSetB)
	_, _ = gB.Apply(opInsA)
	_, _ = gB.Apply(opSetA)

	// Assert convergence across all 5 rows
	for r := 0; r < 5; r++ {
		cvA, okA := gA.GetCell(sheet.CellAddr{Row: r, Col: 0})
		cvB, okB := gB.GetCell(sheet.CellAddr{Row: r, Col: 0})
		if !okA || !okB {
			t.Fatalf("row %d missing cell: okA=%v, okB=%v", r, okA, okB)
		}
		if cvA.Display != cvB.Display {
			t.Fatalf("row %d value mismatch: gA=%q, gB=%q", r, cvA.Display, cvB.Display)
		}
	}
}

func TestConcurrentColInsertBothOrders(t *testing.T) {
	initial := []string{"c0", "c1", "c2"}
	gA := NewGrid("doc-col-both")
	gB := NewGrid("doc-col-both")

	for c, val := range initial {
		op, _ := NewOp("doc-col-both", "init", OpGridSetCell, GridSetCellPayload{Row: 0, Col: c, Raw: val})
		_, _ = gA.Apply(op)
		_, _ = gB.Apply(op)
	}

	opInsA, _ := NewOp("doc-col-both", "actor-A", OpGridInsertAxis, GridInsertAxisPayload{Axis: "col", Index: 1, Count: 1})
	opSetA, _ := NewOp("doc-col-both", "actor-A", OpGridSetCell, GridSetCellPayload{Row: 0, Col: 1, Raw: "A_col"})

	opInsB, _ := NewOp("doc-col-both", "actor-B", OpGridInsertAxis, GridInsertAxisPayload{Axis: "col", Index: 1, Count: 1})
	opSetB, _ := NewOp("doc-col-both", "actor-B", OpGridSetCell, GridSetCellPayload{Row: 0, Col: 1, Raw: "B_col"})

	// gA applies local A, then remote B
	_, _ = gA.Apply(opInsA)
	_, _ = gA.Apply(opSetA)
	_, _ = gA.Apply(opInsB)
	_, _ = gA.Apply(opSetB)

	// gB applies local B, then remote A
	_, _ = gB.Apply(opInsB)
	_, _ = gB.Apply(opSetB)
	_, _ = gB.Apply(opInsA)
	_, _ = gB.Apply(opSetA)

	// Assert convergence across all 5 columns
	for c := 0; c < 5; c++ {
		cvA, okA := gA.GetCell(sheet.CellAddr{Row: 0, Col: c})
		cvB, okB := gB.GetCell(sheet.CellAddr{Row: 0, Col: c})
		if !okA || !okB {
			t.Fatalf("col %d missing cell: okA=%v, okB=%v", c, okA, okB)
		}
		if cvA.Display != cvB.Display {
			t.Fatalf("col %d value mismatch: gA=%q, gB=%q", c, cvA.Display, cvB.Display)
		}
	}
}

func TestConcurrentInsertAndDelete(t *testing.T) {
	initial := []string{"r0", "r1", "r2", "r3"}
	gA := NewGrid("doc-ins-del")
	gB := NewGrid("doc-ins-del")

	for r, val := range initial {
		op, _ := NewOp("doc-ins-del", "init", OpGridSetCell, GridSetCellPayload{Row: r, Col: 0, Raw: val})
		_, _ = gA.Apply(op)
		_, _ = gB.Apply(op)
	}

	// A inserts row at index 2
	opInsA, _ := NewOp("doc-ins-del", "actor-A", OpGridInsertAxis, GridInsertAxisPayload{Axis: "row", Index: 2, Count: 1})
	// B deletes row at index 0
	opDelB, _ := NewOp("doc-ins-del", "actor-B", OpGridDeleteAxis, GridDeleteAxisPayload{Axis: "row", Index: 0, Count: 1})

	// gA applies insert A then delete B
	_, _ = gA.Apply(opInsA)
	_, _ = gA.Apply(opDelB)

	// gB applies delete B then insert A
	_, _ = gB.Apply(opDelB)
	_, _ = gB.Apply(opInsA)

	// Assert row counts and cells converge
	allA := gA.AllCells()
	allB := gB.AllCells()
	if len(allA) != len(allB) {
		t.Fatalf("cell count mismatch: gA=%d, gB=%d", len(allA), len(allB))
	}
	for addr, cvA := range allA {
		cvB, ok := allB[addr]
		if !ok || cvA.Display != cvB.Display {
			t.Fatalf("mismatch at %v: gA=%v, gB=%v", addr, cvA, cvB)
		}
	}
}

func TestConcurrentInsertAndSetCell(t *testing.T) {
	gA := NewGrid("doc-ins-set")
	gB := NewGrid("doc-ins-set")

	op0, _ := NewOp("doc-ins-set", "init", OpGridSetCell, GridSetCellPayload{Row: 0, Col: 0, Raw: "v0"})
	op1, _ := NewOp("doc-ins-set", "init", OpGridSetCell, GridSetCellPayload{Row: 1, Col: 0, Raw: "v1"})
	_, _ = gA.Apply(op0)
	_, _ = gA.Apply(op1)
	_, _ = gB.Apply(op0)
	_, _ = gB.Apply(op1)

	// A inserts a row at index 1
	opInsA, _ := NewOp("doc-ins-set", "actor-A", OpGridInsertAxis, GridInsertAxisPayload{Axis: "row", Index: 1, Count: 1})
	// B updates cell (1, 0) with stable position keys
	opSetB, _ := NewOp("doc-ins-set", "actor-B", OpGridSetCell, GridSetCellPayload{
		Row:    1,
		Col:    0,
		RowPos: DefaultPos(1),
		ColPos: DefaultPos(0),
		Raw:    "v1_updated",
	})

	// gA: insert then set
	_, _ = gA.Apply(opInsA)
	_, _ = gA.Apply(opSetB)

	// gB: set then insert
	_, _ = gB.Apply(opSetB)
	_, _ = gB.Apply(opInsA)

	allA := gA.AllCells()
	allB := gB.AllCells()
	if len(allA) != len(allB) {
		t.Fatalf("cell count mismatch: gA=%d, gB=%d", len(allA), len(allB))
	}
	for addr, cvA := range allA {
		cvB, ok := allB[addr]
		if !ok || cvA.Display != cvB.Display {
			t.Fatalf("mismatch at %v: gA=%v, gB=%v", addr, cvA, cvB)
		}
	}
}

func TestGrid_PositionValidation(t *testing.T) {
	grid := NewGrid("doc-val")
	p1 := DefaultPos(1)
	p2 := DefaultPos(2)

	// Invalid position: pos <= after
	invalidOp, _ := NewOp("doc-val", "u1", OpGridInsertAxis, GridInsertAxisPayload{
		Axis:           "row",
		Index:          1,
		Count:          1,
		Pos:            p1, // violates pos > AfterPosition
		AfterPosition:  p1,
		BeforePosition: p2,
	})

	_, err := grid.Apply(invalidOp)
	if err == nil {
		t.Fatalf("expected error for position <= AfterPosition, got nil")
	}

	// Valid position strictly between
	validPos := Between(p1, p2)
	validOp, _ := NewOp("doc-val", "u1", OpGridInsertAxis, GridInsertAxisPayload{
		Axis:           "row",
		Index:          1,
		Count:          1,
		Pos:            validPos,
		AfterPosition:  p1,
		BeforePosition: p2,
	})

	feed, err := grid.Apply(validOp)
	if err != nil {
		t.Fatalf("unexpected error for valid position: %v", err)
	}
	if feed == nil || !feed.AxesChanged {
		t.Fatalf("expected AxesChanged=true, got %v", feed)
	}
}
