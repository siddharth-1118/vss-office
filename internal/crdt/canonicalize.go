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
	RowPos  Pos             `json:"row_pos,omitempty"`
	ColPos  Pos             `json:"col_pos,omitempty"`
	Value   sheet.CellValue `json:"value"`
	LastMod int64           `json:"last_mod,omitempty"`
	Actor   string          `json:"actor,omitempty"`
}

// CanonicalGridSnapshot is the deterministically ordered JSON representation of a Grid.
type CanonicalGridSnapshot struct {
	DocID        string          `json:"doc_id"`
	RowPositions []Pos           `json:"row_positions,omitempty"`
	ColPositions []Pos           `json:"col_positions,omitempty"`
	Cells        []CanonicalCell `json:"cells"`
}

// ToCanonicalJSON serializes the Grid into deterministic, sorted JSON.
func (g *Grid) ToCanonicalJSON() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	rowIndex := make(map[Pos]int, len(g.rows))
	for i, p := range g.rows {
		rowIndex[p] = i
	}
	colIndex := make(map[Pos]int, len(g.cols))
	for j, p := range g.cols {
		colIndex[p] = j
	}

	var cells []CanonicalCell
	for k, cv := range g.cells {
		r, rOk := rowIndex[k.Row]
		c, cOk := colIndex[k.Col]
		if !rOk || !cOk {
			continue
		}
		cells = append(cells, CanonicalCell{
			Row:     r,
			Col:     c,
			RowPos:  k.Row,
			ColPos:  k.Col,
			Value:   cv,
			LastMod: g.lastModified[k],
			Actor:   g.lastActor[k],
		})
	}

	// Deterministic sort: Row ascending, then Col ascending
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Row != cells[j].Row {
			return cells[i].Row < cells[j].Row
		}
		return cells[i].Col < cells[j].Col
	})

	rowsCopy := make([]Pos, len(g.rows))
	copy(rowsCopy, g.rows)
	colsCopy := make([]Pos, len(g.cols))
	copy(colsCopy, g.cols)

	snapshot := CanonicalGridSnapshot{
		DocID:        g.DocID,
		RowPositions: rowsCopy,
		ColPositions: colsCopy,
		Cells:        cells,
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

	if len(snapshot.RowPositions) > 0 {
		g.rows = make([]Pos, len(snapshot.RowPositions))
		copy(g.rows, snapshot.RowPositions)
	}
	if len(snapshot.ColPositions) > 0 {
		g.cols = make([]Pos, len(snapshot.ColPositions))
		copy(g.cols, snapshot.ColPositions)
	}

	// For snapshots lacking position metadata, migrate deterministically
	maxRow := -1
	maxCol := -1
	for _, c := range snapshot.Cells {
		if c.Row > maxRow {
			maxRow = c.Row
		}
		if c.Col > maxCol {
			maxCol = c.Col
		}
	}
	if maxRow >= 0 && len(g.rows) <= maxRow {
		g.ensureRowLocked(maxRow)
	}
	if maxCol >= 0 && len(g.cols) <= maxCol {
		g.ensureColLocked(maxCol)
	}

	for _, c := range snapshot.Cells {
		rowPos := c.RowPos
		if rowPos == "" {
			rowPos = g.rows[c.Row]
		}
		colPos := c.ColPos
		if colPos == "" {
			colPos = g.cols[c.Col]
		}
		key := CellKey{Row: rowPos, Col: colPos}
		g.cells[key] = c.Value
		if c.LastMod > 0 {
			g.lastModified[key] = c.LastMod
		}
		if c.Actor != "" {
			g.lastActor[key] = c.Actor
		}
	}
	return g, nil
}
