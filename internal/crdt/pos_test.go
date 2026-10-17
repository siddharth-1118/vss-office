package crdt

import (
	"testing"
)

func TestPos_BaseAndEdgeCases(t *testing.T) {
	// Empty both
	first := Between("", "")
	if first != "a0" {
		t.Fatalf("expected 'a0', got %q", first)
	}

	// Between("", "a0")
	beforeA0 := Between("", first)
	if !(beforeA0 < first) {
		t.Fatalf("expected %q < %q", beforeA0, first)
	}

	// Between("a0", "")
	afterA0 := Between(first, "")
	if !(first < afterA0) {
		t.Fatalf("expected %q < %q", first, afterA0)
	}

	// Midpoint between "a0" and "a1"
	a1 := DefaultPos(1)
	mid := Between(first, a1)
	if !(first < mid && mid < a1) {
		t.Fatalf("expected %q < %q < %q", first, mid, a1)
	}
}

func TestPos_SequentialInsertionsBoundedLength(t *testing.T) {
	// 1000 insertions at end
	curr := Between("", "")
	for i := 0; i < 1000; i++ {
		next := Between(curr, "")
		if !(curr < next) {
			t.Fatalf("iteration %d: expected %q < %q", i, curr, next)
		}
		curr = next
	}
	if len(curr) > 5 {
		t.Fatalf("1000 append insertions produced key with unbounded length: %q (len %d)", curr, len(curr))
	}

	// 1000 insertions at start
	currStart := Between("", "")
	for i := 0; i < 1000; i++ {
		prev := Between("", currStart)
		if !(prev < currStart) {
			t.Fatalf("iteration %d: expected %q < %q", i, prev, currStart)
		}
		currStart = prev
	}
	if len(currStart) > 5 {
		t.Fatalf("1000 prepend insertions produced key with unbounded length: %q (len %d)", currStart, len(currStart))
	}

	// 1000 insertions in middle
	left := DefaultPos(0)
	right := DefaultPos(1)
	for i := 0; i < 1000; i++ {
		mid := Between(left, right)
		if !(left < mid && mid < right) {
			t.Fatalf("middle iteration %d: expected %q < %q < %q", i, left, mid, right)
		}
		// Alternate splitting left-half or right-half
		if i%2 == 0 {
			left = mid
		} else {
			right = mid
		}
	}
	if len(left) > 200 {
		t.Fatalf("1000 middle insertions produced key with unbounded length: %q (len %d)", left, len(left))
	}
}

func TestPos_ActorTieBreaker(t *testing.T) {
	p1 := DefaultPos(1)
	p2 := DefaultPos(2)

	posA := BetweenWithActor(p1, p2, "actor-A")
	posB := BetweenWithActor(p1, p2, "actor-B")

	if !(p1 < posA && posA < posB && posB < p2) {
		t.Fatalf("expected %q < %q < %q < %q", p1, posA, posB, p2)
	}
}

func TestPos_ValidatePosBetween(t *testing.T) {
	p1 := DefaultPos(1)
	p2 := DefaultPos(2)
	mid := Between(p1, p2)

	if !ValidatePosBetween(mid, p1, p2) {
		t.Fatalf("expected ValidatePosBetween(%q, %q, %q) == true", mid, p1, p2)
	}
	if ValidatePosBetween(p1, p1, p2) {
		t.Fatalf("expected ValidatePosBetween with pos == after to be false")
	}
	if ValidatePosBetween(p2, p1, p2) {
		t.Fatalf("expected ValidatePosBetween with pos == before to be false")
	}
	if ValidatePosBetween("", p1, p2) {
		t.Fatalf("expected ValidatePosBetween with empty pos to be false")
	}
}

func TestPos_RandomOrderingProperty(t *testing.T) {
	list := []Pos{DefaultPos(0), DefaultPos(10)}
	for i := 0; i < 200; i++ {
		idx := i % (len(list) - 1)
		newPos := Between(list[idx], list[idx+1])
		if !(list[idx] < newPos && newPos < list[idx+1]) {
			t.Fatalf("ordering failure: %q < %q < %q failed", list[idx], newPos, list[idx+1])
		}
		// Insert in list
		list = append(list[:idx+1], append([]Pos{newPos}, list[idx+1:]...)...)
	}

	// Verify entire list is strictly sorted
	for i := 0; i < len(list)-1; i++ {
		if !(list[i] < list[i+1]) {
			t.Fatalf("list not strictly sorted at index %d: %q >= %q", i, list[i], list[i+1])
		}
	}
}
