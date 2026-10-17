package crdt

import (
	"fmt"
	"sort"
	"sync"

	"github.com/vss-suite/vss/server/internal/sheet"
)

// CellKey uniquely identifies a cell in the CRDT Grid by fractional row and col positions.
type CellKey struct {
	Row Pos
	Col Pos
}

// Grid represents a collaborative spreadsheet document backed by CRDT operations.
// Grid implements sheet.CellStorage.
type Grid struct {
	mu           sync.RWMutex
	DocID        string
	rows         []Pos
	cols         []Pos
	cells        map[CellKey]sheet.CellValue
	lastModified map[CellKey]int64
	lastActor    map[CellKey]string

	// actorAxisCache maps an actor's axis edit (e.g. "actor-A:row:2") to its assigned Pos
	actorAxisCache map[string]Pos
}

// NewGrid initializes an empty Grid document.
func NewGrid(docID string) *Grid {
	return &Grid{
		DocID:          docID,
		rows:           make([]Pos, 0),
		cols:           make([]Pos, 0),
		cells:          make(map[CellKey]sheet.CellValue),
		lastModified:   make(map[CellKey]int64),
		lastActor:      make(map[CellKey]string),
		actorAxisCache: make(map[string]Pos),
	}
}

func actorKey(actor, axis string, index int) string {
	return fmt.Sprintf("%s:%s:%d", actor, axis, index)
}

// ensureRowLocked ensures that rows slice has at least r+1 elements. Caller must hold lock.
func (g *Grid) ensureRowLocked(r int) {
	if r < 0 {
		return
	}
	for len(g.rows) <= r {
		idx := len(g.rows)
		g.rows = append(g.rows, DefaultPos(idx))
	}
}

// ensureColLocked ensures that cols slice has at least c+1 elements. Caller must hold lock.
func (g *Grid) ensureColLocked(c int) {
	if c < 0 {
		return
	}
	for len(g.cols) <= c {
		idx := len(g.cols)
		g.cols = append(g.cols, DefaultPos(idx))
	}
}

func (g *Grid) insertRowPos(pos Pos) int {
	idx := sort.Search(len(g.rows), func(i int) bool {
		return g.rows[i] >= pos
	})
	if idx < len(g.rows) && g.rows[idx] == pos {
		return idx
	}
	g.rows = append(g.rows, "")
	copy(g.rows[idx+1:], g.rows[idx:])
	g.rows[idx] = pos
	return idx
}

func (g *Grid) insertColPos(pos Pos) int {
	idx := sort.Search(len(g.cols), func(i int) bool {
		return g.cols[i] >= pos
	})
	if idx < len(g.cols) && g.cols[idx] == pos {
		return idx
	}
	g.cols = append(g.cols, "")
	copy(g.cols[idx+1:], g.cols[idx:])
	g.cols[idx] = pos
	return idx
}

func (g *Grid) rowPosIndex(pos Pos) int {
	idx := sort.Search(len(g.rows), func(i int) bool {
		return g.rows[i] >= pos
	})
	if idx < len(g.rows) && g.rows[idx] == pos {
		return idx
	}
	return -1
}

func (g *Grid) colPosIndex(pos Pos) int {
	idx := sort.Search(len(g.cols), func(i int) bool {
		return g.cols[i] >= pos
	})
	if idx < len(g.cols) && g.cols[idx] == pos {
		return idx
	}
	return -1
}

// RowPositions returns a copy of current row positions in order.
func (g *Grid) RowPositions() []Pos {
	g.mu.RLock()
	defer g.mu.RUnlock()
	res := make([]Pos, len(g.rows))
	copy(res, g.rows)
	return res
}

// ColPositions returns a copy of current col positions in order.
func (g *Grid) ColPositions() []Pos {
	g.mu.RLock()
	defer g.mu.RUnlock()
	res := make([]Pos, len(g.cols))
	copy(res, g.cols)
	return res
}

// GetCell retrieves a cell from the Grid. Implements sheet.CellStorage.
func (g *Grid) GetCell(addr sheet.CellAddr) (sheet.CellValue, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if addr.Row < 0 || addr.Row >= len(g.rows) || addr.Col < 0 || addr.Col >= len(g.cols) {
		return sheet.CellValue{}, false
	}
	key := CellKey{Row: g.rows[addr.Row], Col: g.cols[addr.Col]}
	cv, ok := g.cells[key]
	return cv, ok
}

// SetCell sets a cell directly in the Grid. Implements sheet.CellStorage.
func (g *Grid) SetCell(addr sheet.CellAddr, cv sheet.CellValue) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureRowLocked(addr.Row)
	g.ensureColLocked(addr.Col)
	key := CellKey{Row: g.rows[addr.Row], Col: g.cols[addr.Col]}
	g.cells[key] = cv
}

// AllCells returns a snapshot copy of all cells in the Grid keyed by integer address.
func (g *Grid) AllCells() map[sheet.CellAddr]sheet.CellValue {
	g.mu.RLock()
	defer g.mu.RUnlock()
	res := make(map[sheet.CellAddr]sheet.CellValue, len(g.cells))
	rowIndex := make(map[Pos]int, len(g.rows))
	for i, p := range g.rows {
		rowIndex[p] = i
	}
	colIndex := make(map[Pos]int, len(g.cols))
	for j, p := range g.cols {
		colIndex[p] = j
	}
	for k, v := range g.cells {
		r, rOk := rowIndex[k.Row]
		c, cOk := colIndex[k.Col]
		if rOk && cOk {
			res[sheet.CellAddr{Row: r, Col: c}] = v
		}
	}
	return res
}

// Apply applies an operation to the Grid with CRDT convergence (LWW on cells, fractional indexing on axes).
func (g *Grid) Apply(op Op) (*sheet.ChangeFeed, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	switch op.Type {
	case OpGridSetCell:
		var p GridSetCellPayload
		if err := op.UnmarshalPayload(&p); err != nil {
			return nil, fmt.Errorf("unmarshal set_cell: %w", err)
		}
		if p.Row < 0 || p.Row >= sheet.MaxRow || p.Col < 0 || p.Col >= sheet.MaxCol {
			return nil, fmt.Errorf("cell out of bounds: row=%d, col=%d", p.Row, p.Col)
		}

		var rowPos Pos
		if p.RowPos != "" {
			rowPos = p.RowPos
			g.insertRowPos(rowPos)
		} else {
			ak := actorKey(op.ActorID, "row", p.Row)
			if cached, ok := g.actorAxisCache[ak]; ok {
				rowPos = cached
			} else {
				g.ensureRowLocked(p.Row)
				rowPos = g.rows[p.Row]
			}
		}

		var colPos Pos
		if p.ColPos != "" {
			colPos = p.ColPos
			g.insertColPos(colPos)
		} else {
			ak := actorKey(op.ActorID, "col", p.Col)
			if cached, ok := g.actorAxisCache[ak]; ok {
				colPos = cached
			} else {
				g.ensureColLocked(p.Col)
				colPos = g.cols[p.Col]
			}
		}

		cellKey := CellKey{Row: rowPos, Col: colPos}

		// Last-Write-Wins (LWW) conflict resolution
		lastMod := g.lastModified[cellKey]
		lastActor := g.lastActor[cellKey]
		if op.Timestamp < lastMod || (op.Timestamp == lastMod && op.ActorID < lastActor) {
			// Older concurrent write rejected
			return nil, nil
		}

		g.lastModified[cellKey] = op.Timestamp
		g.lastActor[cellKey] = op.ActorID

		cv := g.cells[cellKey]
		if p.Formula != nil {
			cv.Formula = *p.Formula
		} else if p.Raw != "" {
			// Clearing existing formula when raw value is explicitly set
			cv.Formula = ""
		}
		if p.Raw != "" {
			coerced := sheet.CoerceRaw(p.Raw)
			cv.Display = coerced.ToDisplayString()
			switch coerced.Kind {
			case sheet.KindBlank:
				cv.Kind = "blank"
				cv.Number = nil
			case sheet.KindNumber:
				cv.Kind = "number"
				n := coerced.Num
				cv.Number = &n
			case sheet.KindText:
				cv.Kind = "text"
				cv.Number = nil
			case sheet.KindBool:
				cv.Kind = "bool"
				cv.Number = nil
			case sheet.KindError:
				cv.Kind = "error"
				cv.Error = coerced.Err
				cv.Number = nil
			}
		} else if p.Formula == nil {
			// Clear cell if both raw and formula are empty
			cv = sheet.CellValue{Kind: "blank"}
		}

		g.cells[cellKey] = cv
		visRow := g.rowPosIndex(rowPos)
		visCol := g.colPosIndex(colPos)
		return &sheet.ChangeFeed{Cells: []sheet.CellAddr{{Row: visRow, Col: visCol}}}, nil

	case OpGridInsertAxis:
		var p GridInsertAxisPayload
		if err := op.UnmarshalPayload(&p); err != nil {
			return nil, fmt.Errorf("unmarshal insert_axis: %w", err)
		}
		if p.Count <= 0 {
			return nil, fmt.Errorf("invalid insert count: %d", p.Count)
		}

		// Position validation: if explicit bounds or pos provided, validate strict order
		if p.Pos != "" && (p.AfterPosition != "" || p.BeforePosition != "") {
			if !ValidatePosBetween(p.Pos, p.AfterPosition, p.BeforePosition) {
				return nil, fmt.Errorf("position %q not strictly between %q and %q", p.Pos, p.AfterPosition, p.BeforePosition)
			}
		}

		if p.Axis == "row" {
			g.ensureRowLocked(p.Index)
			for i := 0; i < p.Count; i++ {
				var newPos Pos
				if p.Pos != "" && i == 0 {
					newPos = p.Pos
				} else {
					left := Pos("")
					right := Pos("")
					if p.AfterPosition != "" {
						left = p.AfterPosition
					}
					if p.BeforePosition != "" {
						right = p.BeforePosition
					}
					if left == "" && right == "" {
						if p.Index == 0 {
							right = DefaultPos(0)
						} else {
							left = DefaultPos(p.Index - 1)
							right = DefaultPos(p.Index)
						}
					}
					tie := op.ActorID
					if tie == "" {
						tie = op.ID
					}
					newPos = BetweenWithActor(left, right, tie)
				}
				visIdx := g.insertRowPos(newPos)
				g.actorAxisCache[actorKey(op.ActorID, "row", p.Index+i)] = newPos

				// Rewrite formula references in all existing formula cells
				for ck, cellVal := range g.cells {
					if cellVal.Formula != "" {
						cellVal.Formula = sheet.RewriteFormula(cellVal.Formula, "row", visIdx, 1, false)
						g.cells[ck] = cellVal
					}
				}
			}
			return &sheet.ChangeFeed{AxesChanged: true}, nil

		} else if p.Axis == "col" {
			g.ensureColLocked(p.Index)
			for j := 0; j < p.Count; j++ {
				var newPos Pos
				if p.Pos != "" && j == 0 {
					newPos = p.Pos
				} else {
					left := Pos("")
					right := Pos("")
					if p.AfterPosition != "" {
						left = p.AfterPosition
					}
					if p.BeforePosition != "" {
						right = p.BeforePosition
					}
					if left == "" && right == "" {
						if p.Index == 0 {
							right = DefaultPos(0)
						} else {
							left = DefaultPos(p.Index - 1)
							right = DefaultPos(p.Index)
						}
					}
					tie := op.ActorID
					if tie == "" {
						tie = op.ID
					}
					newPos = BetweenWithActor(left, right, tie)
				}
				visIdx := g.insertColPos(newPos)
				g.actorAxisCache[actorKey(op.ActorID, "col", p.Index+j)] = newPos

				for ck, cellVal := range g.cells {
					if cellVal.Formula != "" {
						cellVal.Formula = sheet.RewriteFormula(cellVal.Formula, "col", visIdx, 1, false)
						g.cells[ck] = cellVal
					}
				}
			}
			return &sheet.ChangeFeed{AxesChanged: true}, nil

		} else {
			return nil, fmt.Errorf("unknown axis: %s", p.Axis)
		}

	case OpGridDeleteAxis:
		var p GridDeleteAxisPayload
		if err := op.UnmarshalPayload(&p); err != nil {
			return nil, fmt.Errorf("unmarshal delete_axis: %w", err)
		}
		if p.Count <= 0 {
			return nil, fmt.Errorf("invalid delete count: %d", p.Count)
		}

		if p.Axis == "row" {
			g.ensureRowLocked(p.Index + p.Count - 1)
			for i := 0; i < p.Count; i++ {
				if p.Index >= len(g.rows) {
					break
				}
				pos := g.rows[p.Index]
				// Delete all cells belonging to this row
				for ck := range g.cells {
					if ck.Row == pos {
						delete(g.cells, ck)
						delete(g.lastModified, ck)
						delete(g.lastActor, ck)
					}
				}
				g.rows = append(g.rows[:p.Index], g.rows[p.Index+1:]...)

				// Rewrite formulas for deleted row
				for ck, cellVal := range g.cells {
					if cellVal.Formula != "" {
						cellVal.Formula = sheet.RewriteFormula(cellVal.Formula, "row", p.Index, 1, true)
						g.cells[ck] = cellVal
					}
				}
			}
			return &sheet.ChangeFeed{AxesChanged: true}, nil

		} else if p.Axis == "col" {
			g.ensureColLocked(p.Index + p.Count - 1)
			for j := 0; j < p.Count; j++ {
				if p.Index >= len(g.cols) {
					break
				}
				pos := g.cols[p.Index]
				for ck := range g.cells {
					if ck.Col == pos {
						delete(g.cells, ck)
						delete(g.lastModified, ck)
						delete(g.lastActor, ck)
					}
				}
				g.cols = append(g.cols[:p.Index], g.cols[p.Index+1:]...)

				for ck, cellVal := range g.cells {
					if cellVal.Formula != "" {
						cellVal.Formula = sheet.RewriteFormula(cellVal.Formula, "col", p.Index, 1, true)
						g.cells[ck] = cellVal
					}
				}
			}
			return &sheet.ChangeFeed{AxesChanged: true}, nil

		} else {
			return nil, fmt.Errorf("unknown axis: %s", p.Axis)
		}

	default:
		return nil, fmt.Errorf("unsupported op type: %s", op.Type)
	}
}
