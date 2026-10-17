package sheet

import (
	"math"
	"testing"
)

func addr(a1 string) CellAddr {
	a, err := Parse(a1)
	if err != nil {
		panic(err)
	}
	return a
}

func TestEngine_IncrementalRecalc(t *testing.T) {
	storage := NewMapCellStorage()
	engine := NewEngine(storage)

	// A1 = 10
	_ = engine.SetCell(addr("A1"), "10", "")
	// B1 = A1 * 2 (20)
	_ = engine.SetCell(addr("B1"), "", "=A1*2")
	// C1 = B1 + 5 (25)
	_ = engine.SetCell(addr("C1"), "", "=B1+5")
	// E1 = 1
	_ = engine.SetCell(addr("E1"), "1", "")
	// D1 = E1 + 100 (101) - independent branch
	_ = engine.SetCell(addr("D1"), "", "=E1+100")

	// Full initial recalculation
	stats, err := engine.RecalculateAll()
	if err != nil {
		t.Fatalf("RecalculateAll failed: %v", err)
	}
	if !stats.Full {
		t.Errorf("expected Full=true, got %v", stats.Full)
	}
	if stats.Evaluated != 3 {
		t.Errorf("expected Evaluated=3, got %d", stats.Evaluated)
	}
	if stats.StaleReads != 0 {
		t.Errorf("expected StaleReads=0, got %d", stats.StaleReads)
	}

	cvB1, _ := storage.GetCell(addr("B1"))
	if cvB1.Number == nil || *cvB1.Number != 20 {
		t.Errorf("expected B1=20, got %v", cvB1)
	}
	cvC1, _ := storage.GetCell(addr("C1"))
	if cvC1.Number == nil || *cvC1.Number != 25 {
		t.Errorf("expected C1=25, got %v", cvC1)
	}
	cvD1, _ := storage.GetCell(addr("D1"))
	if cvD1.Number == nil || *cvD1.Number != 101 {
		t.Errorf("expected D1=101, got %v", cvD1)
	}

	// Incremental update: change A1 from 10 to 20
	_ = engine.SetCell(addr("A1"), "20", "")
	stats, err = engine.Recalculate(ChangeFeed{Cells: []CellAddr{addr("A1")}})
	if err != nil {
		t.Fatalf("incremental Recalculate failed: %v", err)
	}
	if stats.Full {
		t.Errorf("expected Full=false, got true")
	}
	// Only B1 and C1 should be evaluated, NOT D1!
	if stats.Evaluated != 2 {
		t.Errorf("expected Evaluated=2, got %d", stats.Evaluated)
	}
	if stats.StaleReads != 0 {
		t.Errorf("expected StaleReads=0, got %d", stats.StaleReads)
	}

	cvB1, _ = storage.GetCell(addr("B1"))
	if cvB1.Number == nil || *cvB1.Number != 40 {
		t.Errorf("expected B1=40, got %v", cvB1)
	}
	cvC1, _ = storage.GetCell(addr("C1"))
	if cvC1.Number == nil || *cvC1.Number != 45 {
		t.Errorf("expected C1=45, got %v", cvC1)
	}
	cvD1, _ = storage.GetCell(addr("D1"))
	if cvD1.Number == nil || *cvD1.Number != 101 {
		t.Errorf("expected D1=101 unchanged, got %v", cvD1)
	}
}

func TestEngine_LongChainRecalc(t *testing.T) {
	storage := NewMapCellStorage()
	engine := NewEngine(storage)

	// Chain: A1 -> A2 -> A3 -> A4 -> A5
	_ = engine.SetCell(addr("A1"), "1", "")
	_ = engine.SetCell(addr("A2"), "", "=A1+1")
	_ = engine.SetCell(addr("A3"), "", "=A2*2")
	_ = engine.SetCell(addr("A4"), "", "=A3+3")
	_ = engine.SetCell(addr("A5"), "", "=A4*2")

	stats, err := engine.RecalculateAll()
	if err != nil {
		t.Fatalf("RecalculateAll failed: %v", err)
	}
	if stats.StaleReads != 0 {
		t.Errorf("expected StaleReads=0, got %d", stats.StaleReads)
	}
	// A1=1, A2=2, A3=4, A4=7, A5=14
	cvA5, _ := storage.GetCell(addr("A5"))
	if cvA5.Number == nil || *cvA5.Number != 14 {
		t.Errorf("expected A5=14, got %v", cvA5)
	}

	// Update A1 to 3: A2=4, A3=8, A4=11, A5=22
	_ = engine.SetCell(addr("A1"), "3", "")
	stats, err = engine.Recalculate(ChangeFeed{Cells: []CellAddr{addr("A1")}})
	if err != nil {
		t.Fatalf("Recalculate failed: %v", err)
	}
	if stats.Evaluated != 4 {
		t.Errorf("expected Evaluated=4, got %d", stats.Evaluated)
	}
	if stats.StaleReads != 0 {
		t.Errorf("expected StaleReads=0, got %d", stats.StaleReads)
	}
	cvA5, _ = storage.GetCell(addr("A5"))
	if cvA5.Number == nil || *cvA5.Number != 22 {
		t.Errorf("expected A5=22, got %v", cvA5)
	}
}

func TestEngine_CycleDetectionAndBreak(t *testing.T) {
	storage := NewMapCellStorage()
	engine := NewEngine(storage)

	// Cycle: A1 = B1, B1 = A1
	_ = engine.SetCell(addr("A1"), "", "=B1")
	_ = engine.SetCell(addr("B1"), "", "=A1")

	stats, err := engine.RecalculateAll()
	if err != nil {
		t.Fatalf("RecalculateAll failed: %v", err)
	}
	if stats.StaleReads != 0 {
		t.Errorf("expected StaleReads=0, got %d", stats.StaleReads)
	}

	cvA1, _ := storage.GetCell(addr("A1"))
	if cvA1.Kind != "error" || cvA1.Error != ErrCycle {
		t.Errorf("expected A1=#CYCLE!, got %v", cvA1)
	}
	cvB1, _ := storage.GetCell(addr("B1"))
	if cvB1.Kind != "error" || cvB1.Error != ErrCycle {
		t.Errorf("expected B1=#CYCLE!, got %v", cvB1)
	}

	// Break cycle: set B1 to literal number 5
	_ = engine.SetCell(addr("B1"), "5", "")
	stats, err = engine.Recalculate(ChangeFeed{Cells: []CellAddr{addr("B1")}})
	if err != nil {
		t.Fatalf("Recalculate failed: %v", err)
	}
	if stats.StaleReads != 0 {
		t.Errorf("expected StaleReads=0, got %d", stats.StaleReads)
	}

	cvA1, _ = storage.GetCell(addr("A1"))
	if cvA1.Kind != "number" || cvA1.Number == nil || *cvA1.Number != 5 {
		t.Errorf("expected A1=5 after cycle break, got %v", cvA1)
	}
}

func TestEngine_SelfCycle(t *testing.T) {
	storage := NewMapCellStorage()
	engine := NewEngine(storage)

	// Self-referencing cycle: A1 = A1 + 1
	_ = engine.SetCell(addr("A1"), "", "=A1+1")
	stats, err := engine.RecalculateAll()
	if err != nil {
		t.Fatalf("RecalculateAll failed: %v", err)
	}
	if stats.StaleReads != 0 {
		t.Errorf("expected StaleReads=0, got %d", stats.StaleReads)
	}
	cvA1, _ := storage.GetCell(addr("A1"))
	if cvA1.Kind != "error" || cvA1.Error != ErrCycle {
		t.Errorf("expected A1=#CYCLE!, got %v", cvA1)
	}
}

func TestEngine_RangeDependency(t *testing.T) {
	storage := NewMapCellStorage()
	engine := NewEngine(storage)

	_ = engine.SetCell(addr("A1"), "1", "")
	_ = engine.SetCell(addr("A2"), "2", "")
	_ = engine.SetCell(addr("A3"), "3", "")
	_ = engine.SetCell(addr("B1"), "", "=SUM(A1:A3)")

	stats, err := engine.RecalculateAll()
	if err != nil {
		t.Fatalf("RecalculateAll failed: %v", err)
	}
	if stats.StaleReads != 0 {
		t.Errorf("expected StaleReads=0, got %d", stats.StaleReads)
	}
	cvB1, _ := storage.GetCell(addr("B1"))
	if cvB1.Number == nil || *cvB1.Number != 6 {
		t.Errorf("expected B1=6, got %v", cvB1)
	}

	// Change middle cell in range A2 from 2 to 10
	_ = engine.SetCell(addr("A2"), "10", "")
	stats, err = engine.Recalculate(ChangeFeed{Cells: []CellAddr{addr("A2")}})
	if err != nil {
		t.Fatalf("Recalculate failed: %v", err)
	}
	if stats.Evaluated != 1 {
		t.Errorf("expected Evaluated=1, got %d", stats.Evaluated)
	}
	if stats.StaleReads != 0 {
		t.Errorf("expected StaleReads=0, got %d", stats.StaleReads)
	}
	cvB1, _ = storage.GetCell(addr("B1"))
	if cvB1.Number == nil || *cvB1.Number != 14 {
		t.Errorf("expected B1=14, got %v", cvB1)
	}
}

func TestEngine_VolatileRecalc(t *testing.T) {
	storage := NewMapCellStorage()
	engine := NewEngine(storage)

	currentRNG := 0.1
	engine.RNG = func() float64 { return currentRNG }

	_ = engine.SetCell(addr("A1"), "", "=RAND()")
	_ = engine.SetCell(addr("B1"), "", "=A1*100")

	stats, err := engine.RecalculateAll()
	if err != nil {
		t.Fatalf("RecalculateAll failed: %v", err)
	}
	if stats.VolatileCells != 1 {
		t.Errorf("expected VolatileCells=1, got %d", stats.VolatileCells)
	}
	if stats.StaleReads != 0 {
		t.Errorf("expected StaleReads=0, got %d", stats.StaleReads)
	}

	cvA1, _ := storage.GetCell(addr("A1"))
	if cvA1.Number == nil || math.Abs(*cvA1.Number-0.1) > 1e-6 {
		t.Errorf("expected A1=0.1, got %v", cvA1)
	}
	cvB1, _ := storage.GetCell(addr("B1"))
	if cvB1.Number == nil || math.Abs(*cvB1.Number-10.0) > 1e-6 {
		t.Errorf("expected B1=10.0, got %v", cvB1)
	}

	// Change RNG value and trigger incremental recalc with an unrelated cell change
	currentRNG = 0.5
	_ = engine.SetCell(addr("Z100"), "test", "")
	stats, err = engine.Recalculate(ChangeFeed{Cells: []CellAddr{addr("Z100")}})
	if err != nil {
		t.Fatalf("Recalculate failed: %v", err)
	}
	// Volatile cell A1 and dependent B1 must be recalculated!
	if stats.Evaluated < 2 {
		t.Errorf("expected Evaluated>=2 for volatile recalculation, got %d", stats.Evaluated)
	}
	if stats.StaleReads != 0 {
		t.Errorf("expected StaleReads=0, got %d", stats.StaleReads)
	}

	cvA1, _ = storage.GetCell(addr("A1"))
	if cvA1.Number == nil || math.Abs(*cvA1.Number-0.5) > 1e-6 {
		t.Errorf("expected A1=0.5, got %v", cvA1)
	}
	cvB1, _ = storage.GetCell(addr("B1"))
	if cvB1.Number == nil || math.Abs(*cvB1.Number-50.0) > 1e-6 {
		t.Errorf("expected B1=50.0, got %v", cvB1)
	}
}
