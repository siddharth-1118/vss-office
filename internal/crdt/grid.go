package crdt

import (
	"fmt"
	"sync"

	"github.com/vss-suite/vss/server/internal/sheet"
)

// Grid represents a collaborative spreadsheet document backed by CRDT operations.
// Grid implements sheet.CellStorage.
type Grid struct {
	mu           sync.RWMutex
	DocID        string
	cells        map[sheet.CellAddr]sheet.CellValue
	lastModified map[sheet.CellAddr]int64
	lastActor    map[sheet.CellAddr]string
}

// NewGrid initializes an empty Grid document.
func NewGrid(docID string) *Grid {
	return &Grid{
		DocID:        docID,
		cells:        make(map[sheet.CellAddr]sheet.CellValue),
		lastModified: make(map[sheet.CellAddr]int64),
		lastActor:    make(map[sheet.CellAddr]string),
	}
}

// GetCell retrieves a cell from the Grid. Implements sheet.CellStorage.
func (g *Grid) GetCell(addr sheet.CellAddr) (sheet.CellValue, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	cv, ok := g.cells[addr]
	return cv, ok
}

// SetCell sets a cell directly in the Grid. Implements sheet.CellStorage.
func (g *Grid) SetCell(addr sheet.CellAddr, cv sheet.CellValue) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cells[addr] = cv
}

// AllCells returns a snapshot copy of all cells in the Grid.
func (g *Grid) AllCells() map[sheet.CellAddr]sheet.CellValue {
	g.mu.RLock()
	defer g.mu.RUnlock()
	res := make(map[sheet.CellAddr]sheet.CellValue, len(g.cells))
	for k, v := range g.cells {
		res[k] = v
	}
	return res
}

// Apply applies an operation to the Grid with CRDT convergence (LWW on cells).
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

		addr := sheet.CellAddr{Row: p.Row, Col: p.Col}

		// Last-Write-Wins (LWW) conflict resolution
		lastMod := g.lastModified[addr]
		lastActor := g.lastActor[addr]
		if op.Timestamp < lastMod || (op.Timestamp == lastMod && op.ActorID < lastActor) {
			// Older concurrent write rejected
			return nil, nil
		}

		g.lastModified[addr] = op.Timestamp
		g.lastActor[addr] = op.ActorID

		cv := g.cells[addr]
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

		g.cells[addr] = cv
		return &sheet.ChangeFeed{Cells: []sheet.CellAddr{addr}}, nil

	case OpGridInsertAxis:
		var p GridInsertAxisPayload
		if err := op.UnmarshalPayload(&p); err != nil {
			return nil, fmt.Errorf("unmarshal insert_axis: %w", err)
		}
		if p.Count <= 0 {
			return nil, fmt.Errorf("invalid insert count: %d", p.Count)
		}

		newCells := make(map[sheet.CellAddr]sheet.CellValue, len(g.cells))
		newMod := make(map[sheet.CellAddr]int64, len(g.lastModified))
		newActor := make(map[sheet.CellAddr]string, len(g.lastActor))

		if p.Axis == "row" {
			for addr, cv := range g.cells {
				newAddr := addr
				if addr.Row >= p.Index {
					newAddr.Row += p.Count
				}
				if cv.Formula != "" {
					cv.Formula = sheet.RewriteFormula(cv.Formula, "row", p.Index, p.Count, false)
				}
				newCells[newAddr] = cv
				newMod[newAddr] = g.lastModified[addr]
				newActor[newAddr] = g.lastActor[addr]
			}
		} else if p.Axis == "col" {
			for addr, cv := range g.cells {
				newAddr := addr
				if addr.Col >= p.Index {
					newAddr.Col += p.Count
				}
				if cv.Formula != "" {
					cv.Formula = sheet.RewriteFormula(cv.Formula, "col", p.Index, p.Count, false)
				}
				newCells[newAddr] = cv
				newMod[newAddr] = g.lastModified[addr]
				newActor[newAddr] = g.lastActor[addr]
			}
		} else {
			return nil, fmt.Errorf("unknown axis: %s", p.Axis)
		}

		g.cells = newCells
		g.lastModified = newMod
		g.lastActor = newActor
		return &sheet.ChangeFeed{AxesChanged: true}, nil

	case OpGridDeleteAxis:
		var p GridDeleteAxisPayload
		if err := op.UnmarshalPayload(&p); err != nil {
			return nil, fmt.Errorf("unmarshal delete_axis: %w", err)
		}
		if p.Count <= 0 {
			return nil, fmt.Errorf("invalid delete count: %d", p.Count)
		}

		newCells := make(map[sheet.CellAddr]sheet.CellValue, len(g.cells))
		newMod := make(map[sheet.CellAddr]int64, len(g.lastModified))
		newActor := make(map[sheet.CellAddr]string, len(g.lastActor))

		if p.Axis == "row" {
			for addr, cv := range g.cells {
				if addr.Row >= p.Index && addr.Row < p.Index+p.Count {
					// Cell deleted
					continue
				}
				newAddr := addr
				if addr.Row >= p.Index+p.Count {
					newAddr.Row -= p.Count
				}
				if cv.Formula != "" {
					cv.Formula = sheet.RewriteFormula(cv.Formula, "row", p.Index, p.Count, true)
				}
				newCells[newAddr] = cv
				newMod[newAddr] = g.lastModified[addr]
				newActor[newAddr] = g.lastActor[addr]
			}
		} else if p.Axis == "col" {
			for addr, cv := range g.cells {
				if addr.Col >= p.Index && addr.Col < p.Index+p.Count {
					// Cell deleted
					continue
				}
				newAddr := addr
				if addr.Col >= p.Index+p.Count {
					newAddr.Col -= p.Count
				}
				if cv.Formula != "" {
					cv.Formula = sheet.RewriteFormula(cv.Formula, "col", p.Index, p.Count, true)
				}
				newCells[newAddr] = cv
				newMod[newAddr] = g.lastModified[addr]
				newActor[newAddr] = g.lastActor[addr]
			}
		} else {
			return nil, fmt.Errorf("unknown axis: %s", p.Axis)
		}

		g.cells = newCells
		g.lastModified = newMod
		g.lastActor = newActor
		return &sheet.ChangeFeed{AxesChanged: true}, nil

	default:
		return nil, fmt.Errorf("unsupported op type: %s", op.Type)
	}
}
