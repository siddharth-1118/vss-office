package crdt

import (
	"fmt"
	"testing"

	"github.com/vss-suite/vss/server/internal/sheet"
)

// TestStructuralConvergence demonstrates that concurrent structural axis insertions
// currently fail to converge across replicas due to integer coordinate shifting.
// See docs/adr/0002-crdt-structural-convergence.md for root cause analysis and resolution options.
func TestStructuralConvergence(t *testing.T) {
	// Baseline initial state with 3 rows on both replicas
	initialRows := []string{"r0_original", "r1_original", "r2_original"}

	replicaA := NewGrid("doc-convergence-test")
	replicaB := NewGrid("doc-convergence-test")

	for r, val := range initialRows {
		rawVal := val
		op, err := NewOp("doc-convergence-test", "init", OpGridSetCell, GridSetCellPayload{
			Row: r,
			Col: 0,
			Raw: rawVal,
		})
		if err != nil {
			t.Fatalf("failed init op: %v", err)
		}
		if _, err := replicaA.Apply(op); err != nil {
			t.Fatalf("replicaA init failed: %v", err)
		}
		if _, err := replicaB.Apply(op); err != nil {
			t.Fatalf("replicaB init failed: %v", err)
		}
	}

	// Concurrent Edit:
	// Replica A inserts a row at position 2 with content "rA_inserted"
	opInsertA, err := NewOp("doc-convergence-test", "actor-A", OpGridInsertAxis, GridInsertAxisPayload{
		Axis:  "row",
		Index: 2,
		Count: 1,
	})
	if err != nil {
		t.Fatalf("failed opInsertA: %v", err)
	}
	opSetA, err := NewOp("doc-convergence-test", "actor-A", OpGridSetCell, GridSetCellPayload{
		Row: 2,
		Col: 0,
		Raw: "rA_inserted",
	})
	if err != nil {
		t.Fatalf("failed opSetA: %v", err)
	}

	// Replica B concurrently inserts a row at position 2 with content "rB_inserted"
	opInsertB, err := NewOp("doc-convergence-test", "actor-B", OpGridInsertAxis, GridInsertAxisPayload{
		Axis:  "row",
		Index: 2,
		Count: 1,
	})
	if err != nil {
		t.Fatalf("failed opInsertB: %v", err)
	}
	opSetB, err := NewOp("doc-convergence-test", "actor-B", OpGridSetCell, GridSetCellPayload{
		Row: 2,
		Col: 0,
		Raw: "rB_inserted",
	})
	if err != nil {
		t.Fatalf("failed opSetB: %v", err)
	}

	// Local execution on each replica
	if _, err := replicaA.Apply(opInsertA); err != nil {
		t.Fatalf("replicaA local insert failed: %v", err)
	}
	if _, err := replicaA.Apply(opSetA); err != nil {
		t.Fatalf("replicaA local set failed: %v", err)
	}

	if _, err := replicaB.Apply(opInsertB); err != nil {
		t.Fatalf("replicaB local insert failed: %v", err)
	}
	if _, err := replicaB.Apply(opSetB); err != nil {
		t.Fatalf("replicaB local set failed: %v", err)
	}

	// Remote exchange: Merge both directions
	// Replica A receives opInsertB then opSetB
	if _, err := replicaA.Apply(opInsertB); err != nil {
		t.Fatalf("replicaA apply opInsertB failed: %v", err)
	}
	if _, err := replicaA.Apply(opSetB); err != nil {
		t.Fatalf("replicaA apply opSetB failed: %v", err)
	}

	// Replica B receives opInsertA then opSetA
	if _, err := replicaB.Apply(opInsertA); err != nil {
		t.Fatalf("replicaB apply opInsertA failed: %v", err)
	}
	if _, err := replicaB.Apply(opSetA); err != nil {
		t.Fatalf("replicaB apply opSetA failed: %v", err)
	}

	// Inspect and compare final states row by row
	var orderA, orderB []string
	for r := 0; r < 5; r++ {
		cvA, okA := replicaA.GetCell(sheet.CellAddr{Row: r, Col: 0})
		if okA {
			orderA = append(orderA, cvA.Display)
		} else {
			orderA = append(orderA, "<blank>")
		}

		cvB, okB := replicaB.GetCell(sheet.CellAddr{Row: r, Col: 0})
		if okB {
			orderB = append(orderB, cvB.Display)
		} else {
			orderB = append(orderB, "<blank>")
		}
	}

	// Assert cell-by-cell convergence across replicas
	diverged := false
	for r := 0; r < 5; r++ {
		if orderA[r] != orderB[r] || orderA[r] == "<blank>" {
			diverged = true
			break
		}
	}

	if diverged {
		t.Fatalf("CRDT structural divergence detected:\n  replica A has %v\n  replica B has %v\nSee docs/adr/0002-crdt-structural-convergence.md",
			formatOrder(orderA), formatOrder(orderB))
	}
}

func formatOrder(rows []string) string {
	return fmt.Sprintf("[%s]", rows)
}
