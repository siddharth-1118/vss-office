package writer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/vss-suite/vss/server/internal/crdt"
)

// Char represents a single character with fractional index in a text CRDT.
type Char struct {
	ID      string   `json:"id"`
	Pos     crdt.Pos `json:"pos"`
	Value   string   `json:"value"`
	Deleted bool     `json:"deleted,omitempty"`
	ActorID string   `json:"actor_id,omitempty"`
}

// Snapshot represents a serializable document state.
type Snapshot struct {
	DocID      string `json:"doc_id"`
	Chars      []Char `json:"chars"`
	Tombstones []Char `json:"tombstones"`
}

// Doc represents a collaborative text document backed by a fractional-index CRDT.
type Doc struct {
	mu         sync.RWMutex
	DocID      string
	Chars      []Char
	tombstones map[string]bool
}

// NewDoc creates a new empty Doc.
func NewDoc(docID string) *Doc {
	return &Doc{
		DocID:      docID,
		Chars:      make([]Char, 0),
		tombstones: make(map[string]bool),
	}
}

// Len returns the number of visible (non-deleted) characters.
func (d *Doc) Len() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	count := 0
	for _, c := range d.Chars {
		if !c.Deleted {
			count++
		}
	}
	return count
}

// String returns the visible text content of the document.
func (d *Doc) String() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var sb strings.Builder
	for _, c := range d.Chars {
		if !c.Deleted {
			sb.WriteString(c.Value)
		}
	}
	return sb.String()
}

// Insert adds a character maintaining sorted Pos order.
// If char.ID is empty, a UUID is assigned.
func (d *Doc) Insert(char Char) (*Char, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if char.ID == "" {
		char.ID = uuid.NewString()
	}

	if d.tombstones != nil && d.tombstones[char.ID] {
		char.Deleted = true
	}

	// Idempotency: if ID already exists, do not duplicate
	for i := range d.Chars {
		if d.Chars[i].ID == char.ID {
			if char.Deleted {
				d.Chars[i].Deleted = true
			}
			return &d.Chars[i], nil
		}
	}

	// Insert into sorted position based on Pos, with ActorID and ID tie-breakers
	idx := sort.Search(len(d.Chars), func(i int) bool {
		if d.Chars[i].Pos == char.Pos {
			if d.Chars[i].ActorID == char.ActorID {
				return d.Chars[i].ID >= char.ID
			}
			return d.Chars[i].ActorID >= char.ActorID
		}
		return d.Chars[i].Pos >= char.Pos
	})

	d.Chars = append(d.Chars, Char{})
	copy(d.Chars[idx+1:], d.Chars[idx:])
	d.Chars[idx] = char

	return &d.Chars[idx], nil
}

// InsertText inserts a sequence of characters strictly between after and before.
func (d *Doc) InsertText(after, before crdt.Pos, text string, actorID string) ([]Char, error) {
	runes := []rune(text)
	if len(runes) == 0 {
		return nil, nil
	}

	curAfter := after
	inserted := make([]Char, 0, len(runes))
	for _, r := range runes {
		pos := crdt.BetweenWithActor(curAfter, before, actorID)
		ch := Char{
			ID:      uuid.NewString(),
			Pos:     pos,
			Value:   string(r),
			ActorID: actorID,
		}
		res, err := d.Insert(ch)
		if err != nil {
			return nil, err
		}
		inserted = append(inserted, *res)
		curAfter = pos
	}
	return inserted, nil
}

// Delete marks a character with the given ID or Pos as deleted.
func (d *Doc) Delete(idOrPos string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.tombstones == nil {
		d.tombstones = make(map[string]bool)
	}
	d.tombstones[idOrPos] = true

	for i := range d.Chars {
		if d.Chars[i].ID == idOrPos || string(d.Chars[i].Pos) == idOrPos {
			d.Chars[i].Deleted = true
			d.tombstones[d.Chars[i].ID] = true
			return nil
		}
	}
	return nil
}

// DeleteRange marks all characters whose Pos falls within [from, to] (inclusive) as deleted.
// Empty from or to means unbounded on that side.
func (d *Doc) DeleteRange(from, to crdt.Pos) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.tombstones == nil {
		d.tombstones = make(map[string]bool)
	}

	for i := range d.Chars {
		p := d.Chars[i].Pos
		if (from == "" || p >= from) && (to == "" || p <= to) {
			d.Chars[i].Deleted = true
			d.tombstones[d.Chars[i].ID] = true
		}
	}
	return nil
}

// Apply executes a crdt.Op against the text document.
func (d *Doc) Apply(op crdt.Op) error {
	switch op.Type {
	case crdt.OpWriterInsertText:
		var p crdt.WriterInsertTextPayload
		if err := op.UnmarshalPayload(&p); err != nil {
			return fmt.Errorf("unmarshal writer insert_text: %w", err)
		}
		if len(p.Positions) > 0 {
			runes := []rune(p.Text)
			for i, r := range runes {
				var pos crdt.Pos
				if i < len(p.Positions) {
					pos = p.Positions[i]
				} else {
					pos = crdt.BetweenWithActor(p.After, p.Before, op.ActorID)
				}
				ch := Char{
					ID:      fmt.Sprintf("%s:%d", op.ID, i),
					Pos:     pos,
					Value:   string(r),
					ActorID: op.ActorID,
				}
				if _, err := d.Insert(ch); err != nil {
					return err
				}
			}
			return nil
		}

		if p.Pos != "" && len([]rune(p.Text)) <= 1 {
			ch := Char{
				ID:      op.ID,
				Pos:     p.Pos,
				Value:   p.Text,
				ActorID: op.ActorID,
			}
			_, err := d.Insert(ch)
			return err
		}

		_, err := d.InsertText(p.After, p.Before, p.Text, op.ActorID)
		return err

	case crdt.OpWriterDeleteRange:
		var p crdt.WriterDeleteRangePayload
		if err := op.UnmarshalPayload(&p); err != nil {
			return fmt.Errorf("unmarshal writer delete_range: %w", err)
		}
		if len(p.IDs) > 0 {
			for _, id := range p.IDs {
				_ = d.Delete(id)
			}
			return nil
		}
		return d.DeleteRange(p.From, p.To)

	default:
		return fmt.Errorf("unsupported op type for writer: %s", op.Type)
	}
}

// Snapshot captures the current document state into a Snapshot.
func (d *Doc) Snapshot() Snapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()

	chars := make([]Char, 0)
	tombstones := make([]Char, 0)
	for _, c := range d.Chars {
		if c.Deleted {
			tombstones = append(tombstones, c)
		} else {
			chars = append(chars, c)
		}
	}
	return Snapshot{
		DocID:      d.DocID,
		Chars:      chars,
		Tombstones: tombstones,
	}
}

// ToSnapshotJSON returns the JSON encoding of the document snapshot.
func (d *Doc) ToSnapshotJSON() ([]byte, error) {
	snap := d.Snapshot()
	return json.MarshalIndent(snap, "", "  ")
}

// FromSnapshot restores a Doc from a Snapshot.
func FromSnapshot(snap Snapshot) (*Doc, error) {
	doc := NewDoc(snap.DocID)
	for _, c := range snap.Tombstones {
		doc.tombstones[c.ID] = true
		c.Deleted = true
		if _, err := doc.Insert(c); err != nil {
			return nil, err
		}
	}
	for _, c := range snap.Chars {
		c.Deleted = false
		if _, err := doc.Insert(c); err != nil {
			return nil, err
		}
	}
	return doc, nil
}

// FromSnapshotJSON restores a Doc from JSON data.
func FromSnapshotJSON(data []byte) (*Doc, error) {
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return FromSnapshot(snap)
}
