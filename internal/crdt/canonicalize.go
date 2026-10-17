package crdt

import (
	"encoding/json"
	"sort"

	"github.com/vss-suite/vss/server/internal/sheet"
)

// CanonicalCell represents a single cell in the canonical snapshot.
type CanonicalCell struct {
	Row     int             `json:"row"`
	Col     int             `json:"col"`
	Value   sheet.CellValue `json:"value"`
	LastMod int64           `json:"last_mod,omitempty"`
	Actor   string          `json:"actor,omitempty"`
}

// CanonicalGridSnapshot is the deterministically ordered JSON representation of a Grid.
type CanonicalGridSnapshot struct {
	DocID string          `json:"doc_id"`
	Cells []CanonicalCell `json:"cells"`
}

// ToCanonicalJSON serializes the Grid into deterministic, sorted JSON.
func (g *Grid) ToCanonicalJSON() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var cells []CanonicalCell
	for addr, cv := range g.cells {
		cells = append(cells, CanonicalCell{
			Row:     addr.Row,
			Col:     addr.Col,
			Value:   cv,
			LastMod: g.lastModified[addr],
			Actor:   g.lastActor[addr],
		})
	}

	// Deterministic sort: Row ascending, then Col ascending
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Row != cells[j].Row {
			return cells[i].Row < cells[j].Row
		}
		return cells[i].Col < cells[j].Col
	})

	snapshot := CanonicalGridSnapshot{
		DocID: g.DocID,
		Cells: cells,
	}

	return json.MarshalIndent(snapshot, "", "  ")
}

// FromCanonicalJSON reconstructs a Grid from a canonical JSON snapshot.
func FromCanonicalJSON(data []byte) (*Grid, error) {
	var snapshot CanonicalGridSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}

	g := NewGrid(snapshot.DocID)
	for _, c := range snapshot.Cells {
		addr := sheet.CellAddr{Row: c.Row, Col: c.Col}
		g.cells[addr] = c.Value
		if c.LastMod > 0 {
			g.lastModified[addr] = c.LastMod
		}
		if c.Actor != "" {
			g.lastActor[addr] = c.Actor
		}
	}
	return g, nil
}
