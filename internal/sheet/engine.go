package sheet

import (
	"math/rand"
	"strconv"
	"strings"
	"time"
)

// FormulaVersion defines the current formula evaluation engine version.
const FormulaVersion = "sheet/1"

var DefaultVolatiles = map[string]bool{
	"RAND":        true,
	"RANDBETWEEN": true,
	"NOW":         true,
	"TODAY":       true,
}

// CellStorage is the abstraction for reading and writing cell values.
type CellStorage interface {
	GetCell(addr CellAddr) (CellValue, bool)
	SetCell(addr CellAddr, cv CellValue)
}

// RangeStorage is an optional extension for storage backends with efficient range queries.
type RangeStorage interface {
	GetRange(area Area) []Value
}

// AllCellsProvider is an optional extension for discovering all cells.
type AllCellsProvider interface {
	AllCells() map[CellAddr]CellValue
}

// MapCellStorage is an in-memory CellStorage implementation.
type MapCellStorage struct {
	cells map[CellAddr]CellValue
}

func NewMapCellStorage() *MapCellStorage {
	return &MapCellStorage{cells: make(map[CellAddr]CellValue)}
}

func (m *MapCellStorage) GetCell(addr CellAddr) (CellValue, bool) {
	cv, ok := m.cells[addr]
	return cv, ok
}

func (m *MapCellStorage) SetCell(addr CellAddr, cv CellValue) {
	m.cells[addr] = cv
}

func (m *MapCellStorage) AllCells() map[CellAddr]CellValue {
	res := make(map[CellAddr]CellValue, len(m.cells))
	for k, v := range m.cells {
		res[k] = v
	}
	return res
}

// ChangeFeed represents changes to cells or axes in the spreadsheet.
type ChangeFeed struct {
	Cells       []CellAddr
	AxesChanged bool
}

// Stats captures metrics from a recalculation run.
type Stats struct {
	Evaluated     int
	TotalFormulas int
	Full          bool
	VolatileCells int
	StaleReads    int
	Changed       []CellAddr
}

// Engine manages cell formulas, dependency graphs, and incremental recalculation.
type Engine struct {
	storage       CellStorage
	formulas      map[CellAddr]string
	astCache      map[CellAddr]Node
	precedents    map[CellAddr][]CellAddr
	dependents    map[CellAddr]map[CellAddr]bool
	volatileCells map[CellAddr]bool

	Clock func() time.Time
	RNG   func() float64
}

// NewEngine creates an Engine wrapping the provided CellStorage.
func NewEngine(storage CellStorage) *Engine {
	return &Engine{
		storage:       storage,
		formulas:      make(map[CellAddr]string),
		astCache:      make(map[CellAddr]Node),
		precedents:    make(map[CellAddr][]CellAddr),
		dependents:    make(map[CellAddr]map[CellAddr]bool),
		volatileCells: make(map[CellAddr]bool),
		Clock:         time.Now,
		RNG:           rand.Float64,
	}
}

// SetCell updates a cell with either a formula or raw literal, and registers it.
func (e *Engine) SetCell(addr CellAddr, raw string, formula string) error {
	if formula != "" {
		if !strings.HasPrefix(formula, "=") {
			formula = "=" + formula
		}
		node, err := ParseFormula(formula)
		if err != nil {
			cv := CellValue{
				Kind:    "error",
				Error:   err.Error(),
				Formula: formula,
				Display: err.Error(),
			}
			e.storage.SetCell(addr, cv)
			e.removeFormula(addr)
			return err
		}
		e.updateFormulaWithAST(addr, formula, node)
	} else {
		e.removeFormula(addr)
		val := CoerceRaw(raw)
		e.storage.SetCell(addr, val.ToCellValue(""))
	}
	return nil
}

// SetFormula registers or updates a formula for a cell.
func (e *Engine) SetFormula(addr CellAddr, formula string) error {
	if !strings.HasPrefix(formula, "=") {
		formula = "=" + formula
	}
	node, err := ParseFormula(formula)
	if err != nil {
		cv := CellValue{
			Kind:    "error",
			Error:   err.Error(),
			Formula: formula,
			Display: err.Error(),
		}
		e.storage.SetCell(addr, cv)
		e.removeFormula(addr)
		return err
	}
	e.updateFormulaWithAST(addr, formula, node)
	return nil
}

// ClearFormula unregisters a formula from a cell.
func (e *Engine) ClearFormula(addr CellAddr) {
	e.removeFormula(addr)
}

func (e *Engine) updateFormulaWithAST(addr CellAddr, formula string, node Node) {
	e.removeFormula(addr)

	e.formulas[addr] = formula
	e.astCache[addr] = node

	refs := Refs(node)
	e.precedents[addr] = refs
	for _, ref := range refs {
		if e.dependents[ref] == nil {
			e.dependents[ref] = make(map[CellAddr]bool)
		}
		e.dependents[ref][addr] = true
	}

	if HasVolatile(node, DefaultVolatiles) {
		e.volatileCells[addr] = true
	}
}

func (e *Engine) removeFormula(addr CellAddr) {
	delete(e.formulas, addr)
	delete(e.astCache, addr)
	delete(e.volatileCells, addr)

	for _, p := range e.precedents[addr] {
		if e.dependents[p] != nil {
			delete(e.dependents[p], addr)
			if len(e.dependents[p]) == 0 {
				delete(e.dependents, p)
			}
		}
	}
	delete(e.precedents, addr)
}

// Precedents returns a copy of the precedent cell addresses for a formula cell.
func (e *Engine) Precedents(addr CellAddr) []CellAddr {
	if refs, ok := e.precedents[addr]; ok {
		res := make([]CellAddr, len(refs))
		copy(res, refs)
		return res
	}
	return nil
}

// RecalculateAll triggers a full recalculation of all formula cells.
func (e *Engine) RecalculateAll() (Stats, error) {
	return e.Recalculate(ChangeFeed{AxesChanged: true})
}

// Recalculate executes incremental recalculation based on changed cells.
func (e *Engine) Recalculate(feed ChangeFeed) (Stats, error) {
	stats := Stats{
		TotalFormulas: len(e.formulas),
		VolatileCells: len(e.volatileCells),
	}

	// Sync any formulas changed via storage for feed cells
	for _, c := range feed.Cells {
		if cv, ok := e.storage.GetCell(c); ok {
			if cv.Formula != "" && cv.Formula != e.formulas[c] {
				node, err := ParseFormula(cv.Formula)
				if err == nil {
					e.updateFormulaWithAST(c, cv.Formula, node)
				}
			} else if cv.Formula == "" && e.formulas[c] != "" {
				e.removeFormula(c)
			}
		} else if e.formulas[c] != "" {
			e.removeFormula(c)
		}
	}
	stats.TotalFormulas = len(e.formulas)
	stats.VolatileCells = len(e.volatileCells)

	// Determine dirty set
	dirtyFormulas := make(map[CellAddr]bool)
	if feed.AxesChanged || len(feed.Cells) == 0 {
		stats.Full = true
		for c := range e.formulas {
			dirtyFormulas[c] = true
		}
	} else {
		stats.Full = false
		dirty := make(map[CellAddr]bool)
		queue := make([]CellAddr, 0, len(feed.Cells)+len(e.volatileCells))

		// Seed queue with all volatile formula cells
		for vc := range e.volatileCells {
			queue = append(queue, vc)
		}

		// Seed queue with changed cells (if formula) and their immediate dependents
		for _, c := range feed.Cells {
			if e.formulas[c] != "" {
				queue = append(queue, c)
			}
			for dep := range e.dependents[c] {
				queue = append(queue, dep)
			}
		}

		// BFS to find all transitive dependents
		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			if dirty[curr] {
				continue
			}
			dirty[curr] = true
			for dep := range e.dependents[curr] {
				if !dirty[dep] {
					queue = append(queue, dep)
				}
			}
		}

		for c := range dirty {
			if e.formulas[c] != "" {
				dirtyFormulas[c] = true
			}
		}
	}

	if len(dirtyFormulas) == 0 {
		return stats, nil
	}

	// Cycle detection and topological sort of SCCs using Tarjan's algorithm
	sccs := e.tarjanSCC(dirtyFormulas)

	// Tarjan returns SCCs in reverse topological order (sinks to sources).
	// Reversing gives sources to sinks (precedents evaluated before dependents).
	for i, j := 0, len(sccs)-1; i < j; i, j = i+1, j-1 {
		sccs[i], sccs[j] = sccs[j], sccs[i]
	}

	pending := make(map[CellAddr]bool, len(dirtyFormulas))
	for c := range dirtyFormulas {
		pending[c] = true
	}

	ctx := &engineEvalContext{
		engine:  e,
		pending: pending,
		stats:   &stats,
		now:     e.Clock(),
		randVal: e.RNG(),
	}

	for _, scc := range sccs {
		if e.isSCCCycle(scc, dirtyFormulas) {
			// Cycle detected: mark all cells in SCC with #CYCLE!
			for _, cell := range scc {
				delete(pending, cell)
				stats.Evaluated++
				newCV := NewError(ErrCycle).ToCellValue(e.formulas[cell])
				oldCV, found := e.storage.GetCell(cell)
				if !found || !cellValueEqual(oldCV, newCV) {
					e.storage.SetCell(cell, newCV)
					stats.Changed = append(stats.Changed, cell)
				}
			}
		} else {
			// Single non-cyclic node: evaluate in topological order
			cell := scc[0]
			ast := e.astCache[cell]
			val := Evaluate(ast, ctx)
			delete(pending, cell)
			stats.Evaluated++

			newCV := val.ToCellValue(e.formulas[cell])
			oldCV, found := e.storage.GetCell(cell)
			if !found || !cellValueEqual(oldCV, newCV) {
				e.storage.SetCell(cell, newCV)
				stats.Changed = append(stats.Changed, cell)
			}
		}
	}

	return stats, nil
}

func (e *Engine) isSCCCycle(scc []CellAddr, dirty map[CellAddr]bool) bool {
	if len(scc) > 1 {
		return true
	}
	// Self-loop check for single-node SCC
	u := scc[0]
	for _, p := range e.precedents[u] {
		if p == u {
			return true
		}
	}
	return false
}

// tarjanSCC computes Strongly Connected Components in the dirtyFormulas subgraph.
func (e *Engine) tarjanSCC(dirty map[CellAddr]bool) [][]CellAddr {
	var sccs [][]CellAddr
	index := 0
	indices := make(map[CellAddr]int, len(dirty))
	lowlink := make(map[CellAddr]int, len(dirty))
	onStack := make(map[CellAddr]bool, len(dirty))
	var stack []CellAddr

	var strongConnect func(u CellAddr)
	strongConnect = func(u CellAddr) {
		indices[u] = index
		lowlink[u] = index
		index++
		stack = append(stack, u)
		onStack[u] = true

		// Outgoing edges in evaluation order: u -> dep (u must be evaluated before dep)
		for dep := range e.dependents[u] {
			if !dirty[dep] {
				continue
			}
			if _, visited := indices[dep]; !visited {
				strongConnect(dep)
				if lowlink[dep] < lowlink[u] {
					lowlink[u] = lowlink[dep]
				}
			} else if onStack[dep] {
				if indices[dep] < lowlink[u] {
					lowlink[u] = indices[dep]
				}
			}
		}

		if lowlink[u] == indices[u] {
			var scc []CellAddr
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == u {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}

	for u := range dirty {
		if _, visited := indices[u]; !visited {
			strongConnect(u)
		}
	}

	return sccs
}

type engineEvalContext struct {
	engine  *Engine
	pending map[CellAddr]bool
	stats   *Stats
	now     time.Time
	randVal float64
}

func (c *engineEvalContext) Resolve(addr CellAddr) Value {
	// If a formula reads a cell that is scheduled to be recalculated but hasn't been yet,
	// that constitutes a stale read!
	if c.pending[addr] {
		c.stats.StaleReads++
	}
	cv, ok := c.engine.storage.GetCell(addr)
	if !ok {
		return NewBlank()
	}
	return cv.ToValue()
}

func (c *engineEvalContext) ResolveArea(area Area) []Value {
	if rs, ok := c.engine.storage.(RangeStorage); ok {
		return rs.GetRange(area)
	}
	var vals []Value
	for _, cell := range area.Cells() {
		vals = append(vals, c.Resolve(cell))
	}
	return vals
}

func (c *engineEvalContext) Now() time.Time {
	return c.now
}

func (c *engineEvalContext) Rand() float64 {
	return c.randVal
}

func (c *engineEvalContext) IsVolatile(name string) bool {
	return DefaultVolatiles[strings.ToUpper(name)]
}

func cellValueEqual(a, b CellValue) bool {
	if a.Kind != b.Kind || a.Display != b.Display || a.Error != b.Error || a.Formula != b.Formula {
		return false
	}
	if (a.Number == nil) != (b.Number == nil) {
		return false
	}
	if a.Number != nil && b.Number != nil && *a.Number != *b.Number {
		return false
	}
	return true
}

func CoerceRaw(raw string) Value {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return NewBlank()
	}
	upper := strings.ToUpper(raw)
	if upper == "TRUE" {
		return NewBool(true)
	}
	if upper == "FALSE" {
		return NewBool(false)
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err == nil {
		return NewNumber(f)
	}
	return NewText(raw)
}
