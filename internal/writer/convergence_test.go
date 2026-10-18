package writer

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/vss-suite/vss/server/internal/crdt"
)

func TestWriterConvergence_ConcurrentTwoReplicas(t *testing.T) {
	docID := "doc-conv-2"
	replicaA := NewDoc(docID)
	replicaB := NewDoc(docID)

	// Initial common baseline: "START END"
	initChars, err := replicaA.InsertText("", "", "START END", "init")
	if err != nil {
		t.Fatalf("init text on replicaA failed: %v", err)
	}
	for _, c := range initChars {
		if _, err := replicaB.Insert(c); err != nil {
			t.Fatalf("init sync to replicaB failed: %v", err)
		}
	}
	if replicaA.String() != replicaB.String() {
		t.Fatalf("initial baseline out of sync")
	}

	posAfterStart := initChars[4].Pos // 'T'
	posBeforeEnd := initChars[6].Pos  // 'E'

	// Replica A inserts "-A-" concurrently between START and END
	charsA, err := replicaA.InsertText(posAfterStart, posBeforeEnd, "-A-", "actor-A")
	if err != nil {
		t.Fatalf("replicaA insert failed: %v", err)
	}

	// Replica B inserts "-B-" concurrently between START and END
	charsB, err := replicaB.InsertText(posAfterStart, posBeforeEnd, "-B-", "actor-B")
	if err != nil {
		t.Fatalf("replicaB insert failed: %v", err)
	}

	// Cross-replicate ops: A applies B's edits, B applies A's edits
	for _, c := range charsB {
		if _, err := replicaA.Insert(c); err != nil {
			t.Fatalf("replicaA apply charsB failed: %v", err)
		}
	}
	for _, c := range charsA {
		if _, err := replicaB.Insert(c); err != nil {
			t.Fatalf("replicaB apply charsA failed: %v", err)
		}
	}

	strA := replicaA.String()
	strB := replicaB.String()
	if strA != strB {
		t.Fatalf("convergence failure: replicaA=%q != replicaB=%q", strA, strB)
	}
}

func TestWriterConvergence_ThreeReplicasRandomized(t *testing.T) {
	docID := "doc-conv-3"
	replicaA := NewDoc(docID)
	replicaB := NewDoc(docID)
	replicaC := NewDoc(docID)

	// Collect generated operations
	type editOp struct {
		isDelete bool
		char     Char
	}
	var allOps []editOp

	rng := rand.New(rand.NewSource(42))
	var generatedChars []Char
	var lastPos crdt.Pos

	actors := []string{"actor-1", "actor-2", "actor-3"}

	// Generate 30 sequential & concurrent insertions
	for i := 0; i < 30; i++ {
		actor := actors[i%len(actors)]
		val := string(rune('a' + (i % 26)))
		pos := crdt.BetweenWithActor(lastPos, "", actor)
		ch := Char{
			ID:      fmt.Sprintf("ch-%d", i),
			Pos:     pos,
			Value:   val,
			ActorID: actor,
		}
		generatedChars = append(generatedChars, ch)
		allOps = append(allOps, editOp{isDelete: false, char: ch})
		lastPos = pos
	}

	// Generate 5 random deletions of previously inserted chars
	for i := 0; i < 5; i++ {
		idx := rng.Intn(len(generatedChars))
		target := generatedChars[idx]
		target.Deleted = true
		allOps = append(allOps, editOp{isDelete: true, char: target})
	}

	// Apply in order to Replica A
	for _, op := range allOps {
		if op.isDelete {
			_ = replicaA.Delete(op.char.ID)
		} else {
			_, _ = replicaA.Insert(op.char)
		}
	}

	// Apply in reverse order to Replica B
	for i := len(allOps) - 1; i >= 0; i-- {
		op := allOps[i]
		if op.isDelete {
			_ = replicaB.Delete(op.char.ID)
		} else {
			_, _ = replicaB.Insert(op.char)
		}
	}

	// Apply in shuffled order to Replica C
	shuffled := make([]editOp, len(allOps))
	copy(shuffled, allOps)
	rng.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	for _, op := range shuffled {
		if op.isDelete {
			_ = replicaC.Delete(op.char.ID)
		} else {
			_, _ = replicaC.Insert(op.char)
		}
	}

	strA := replicaA.String()
	strB := replicaB.String()
	strC := replicaC.String()

	if strA != strB {
		t.Fatalf("convergence failure A vs B: %q vs %q", strA, strB)
	}
	if strB != strC {
		t.Fatalf("convergence failure B vs C: %q vs %q", strB, strC)
	}
}

func TestWriterConvergence_InterleavedInsertsAndDeletes(t *testing.T) {
	docID := "doc-interleaved"
	rep1 := NewDoc(docID)
	rep2 := NewDoc(docID)

	ch1, _ := rep1.Insert(Char{ID: "1", Pos: crdt.Pos("a0:1"), Value: "X", ActorID: "1"})
	ch2, _ := rep1.Insert(Char{ID: "2", Pos: crdt.Pos("a1:1"), Value: "Y", ActorID: "1"})
	ch3, _ := rep1.Insert(Char{ID: "3", Pos: crdt.Pos("a2:1"), Value: "Z", ActorID: "1"})

	// Sync initial state to rep2
	_, _ = rep2.Insert(*ch1)
	_, _ = rep2.Insert(*ch2)
	_, _ = rep2.Insert(*ch3)

	// Concurrent actions:
	// rep1 deletes 'Y' (ID "2")
	_ = rep1.Delete("2")

	// rep2 concurrently inserts 'W' between 'Y' and 'Z'
	posW := crdt.BetweenWithActor(ch2.Pos, ch3.Pos, "2")
	chW, _ := rep2.Insert(Char{ID: "4", Pos: posW, Value: "W", ActorID: "2"})

	// Merge rep1 changes to rep2
	_ = rep2.Delete("2")

	// Merge rep2 changes to rep1
	_, _ = rep1.Insert(*chW)

	if rep1.String() != rep2.String() {
		t.Fatalf("interleaved convergence mismatch: rep1=%q, rep2=%q", rep1.String(), rep2.String())
	}
	if rep1.String() != "XWZ" {
		t.Fatalf("expected 'XWZ', got %q", rep1.String())
	}
}
