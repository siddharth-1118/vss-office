# VSS Sheet Engine

## Package layout
- `internal/sheet`: Core formula lexer, AST parser, topological dependency graph, cycle detection, and incremental recalculation engine.
- `internal/crdt`: Conflict-free replicated data types for grid state, operation framing, deterministic canonical snapshotting, and LWW convergence.
- `internal/collab`: Multi-client WebSocket collaboration hub, per-connection write serialization, authenticated session handling, and op/values broadcasting.
- `internal/identity`: User model, in-memory user store, bcrypt password hashing, and HS256 JWT issuance/verification service.
- `internal/api`: HTTP REST endpoints (`/healthz`, `/api/docs/`), WebSocket gateway (`/ws`), and end-to-end server integration.

## Public API

Exact signatures from code (`internal/sheet/` and `internal/crdt/`):

- **Constructor (`sheet.New` equivalent)**:
```go
func NewEngine(storage CellStorage) *Engine
```
*(file: [internal/sheet/engine.go:92](file:///E:/projects/vss/server/internal/sheet/engine.go#L92))*

- **Incremental recalculation (`(*Engine).Recalc` equivalent)**:
```go
func (e *Engine) Recalculate(feed ChangeFeed) (Stats, error)
```
*(file: [internal/sheet/engine.go:210](file:///E:/projects/vss/server/internal/sheet/engine.go#L210))*

- **Full recalculation (`(*Engine).RecalcFull` equivalent)**:
```go
func (e *Engine) RecalculateAll() (Stats, error)
```
*(file: [internal/sheet/engine.go:205](file:///E:/projects/vss/server/internal/sheet/engine.go#L205))*

- **Cell retrieval (`Value`, `ValueAt`, `FormulaAt`, `CellAt` equivalents)**:
```go
func (g *Grid) GetCell(addr sheet.CellAddr) (sheet.CellValue, bool)
```
*(file: [internal/crdt/grid.go:31](file:///E:/projects/vss/server/internal/crdt/grid.go#L31))*

- **All values snapshot (`Values` equivalent)**:
```go
func (g *Grid) AllCells() map[sheet.CellAddr]sheet.CellValue
```
*(file: [internal/crdt/grid.go:46](file:///E:/projects/vss/server/internal/crdt/grid.go#L46))*

- **Cell mutation on engine**:
```go
func (e *Engine) SetCell(addr CellAddr, raw string, formula string) error
```
*(file: [internal/sheet/engine.go:103](file:///E:/projects/vss/server/internal/sheet/engine.go#L103))*

```go
func (e *Engine) SetFormula(addr CellAddr, formula string) error
```
*(file: [internal/sheet/engine.go:130](file:///E:/projects/vss/server/internal/sheet/engine.go#L130))*

```go
func (e *Engine) ClearFormula(addr CellAddr)
```
*(file: [internal/sheet/engine.go:151](file:///E:/projects/vss/server/internal/sheet/engine.go#L151))*

```go
func (e *Engine) Precedents(addr CellAddr) []CellAddr
```
*(file: [internal/sheet/engine.go:195](file:///E:/projects/vss/server/internal/sheet/engine.go#L195))*

- **Volatile functions table (`VolatileCount` basis)**:
```go
var DefaultVolatiles = map[string]bool{
```
*(file: [internal/sheet/engine.go:13](file:///E:/projects/vss/server/internal/sheet/engine.go#L13))*

- **Stats & Metrics (`Stats` struct)**:
```go
type Stats struct {
```
*(file: [internal/sheet/engine.go:68](file:///E:/projects/vss/server/internal/sheet/engine.go#L68))*

- **Change Feed input**:
```go
type ChangeFeed struct {
```
*(file: [internal/sheet/engine.go:63](file:///E:/projects/vss/server/internal/sheet/engine.go#L63))*

## Recalculation model

- **ChangeFeed semantics**:
  `ChangeFeed` conveys changes that trigger recalculation:
  - `Cells []CellAddr`: Specific coordinates that were directly modified.
  - `AxesChanged bool`: True when rows or columns were inserted/deleted or an explicit full recomputation is required.
- **Incremental vs full**:
  - *Incremental*: Executes when `feed.AxesChanged == false` and `len(feed.Cells) > 0`. The engine traces the transitive dependents of `feed.Cells` via breadth-first search on `dependents map[CellAddr]map[CellAddr]bool`, builds a dirty subgraph, seeds it with any volatile formula cells, and evaluates only that subgraph in topological order via Tarjan's strongly connected components algorithm (`tarjanSCC`).
  - *Full*: Executes when `feed.AxesChanged == true` or `len(feed.Cells) == 0` (such as on document load via `RecalculateAll()`). All registered formulas in `formulas map[CellAddr]string` are marked dirty and evaluated in topological order.
- **Volatile re-evaluation**:
  - Registered in `DefaultVolatiles`: `RAND`, `RANDBETWEEN`, `NOW`, `TODAY`.
  - Detected at formula registration via AST inspection (`HasVolatile`).
  - Stored in `e.volatileCells map[CellAddr]bool`.
  - On every incremental recalculation, all cells in `e.volatileCells` and their transitive dependents are automatically seeded into the dirty evaluation queue regardless of which cells are listed in `feed.Cells`.
- **StaleReads as a self-check**:
  - During evaluation, `engineEvalContext` maintains `pending map[CellAddr]bool` containing all dirty cells scheduled for recalculation.
  - When a formula reads a cell via `Resolve(addr)`, if `pending[addr] == true`, the engine increments `stats.StaleReads++`.
  - A nonzero `StaleReads` indicates an ordering defect: a dependent cell attempted to read a precedent before the precedent finished recalculation. In a correct topological evaluation, `StaleReads` is strictly 0.

## CRDT model

- **Op kinds and payloads**:
  - `grid.set_cell` (`OpGridSetCell`): Payload `GridSetCellPayload{Row, Col, Raw, Formula *string, Style}`. Sets raw value or formula on a cell.
  - `grid.insert_axis` (`OpGridInsertAxis`): Payload `GridInsertAxisPayload{Axis ("row"|"col"), Index, Count}`. Inserts rows/columns at the designated index.
  - `grid.delete_axis` (`OpGridDeleteAxis`): Payload `GridDeleteAxisPayload{Axis ("row"|"col"), Index, Count}`. Deletes rows/columns starting at index.
- **Canonicalization semantics**:
  - `ToCanonicalJSON()` produces a deterministically sorted snapshot: cells ordered strictly ascending by `Row`, then `Col`.
  - Strips runtime memory pointers and outputs stable, canonical JSON suitable for hash verification, disk storage, and cross-replica state sync.
  - `FromCanonicalJSON()` parses canonical JSON back into a memory `Grid`.
- **TouchedCells**:
  - `OpGridSetCell`: Touches the single target cell `CellAddr{Row: p.Row, Col: p.Col}`.
  - `OpGridInsertAxis`: Touches all cells at or following the inserted axis (`addr.Row >= p.Index` or `addr.Col >= p.Index`).
  - `OpGridDeleteAxis`: Touches all cells in the deleted interval (deleting them) and shifts all subsequent cells (`addr.Row >= p.Index + p.Count`).
- **Convergence for scalar writes**:
  - Last-Write-Wins (LWW) conflict resolution per cell coordinate.
  - Order rule: Higher `op.Timestamp` (Unix milliseconds) wins. On identical timestamp, lexicographically higher `op.ActorID` wins (`op.Timestamp < lastMod || (op.Timestamp == lastMod && op.ActorID < lastActor)` rejects the concurrent write).
- **Convergence for structural ops**:
  - Implemented via fractional-index positions (`crdt.Pos`) for rows and columns (ADR 0002 Accepted).
  - Inserting an axis generates a deterministic orderable position between neighboring keys using base-62 fractional indexing with actor ID tie-breaking.
  - Cells are stored under `CellKey{Row: Pos, Col: Pos}`, ensuring operations commute regardless of network arrival order.
  - See [docs/adr/0002-crdt-structural-convergence.md](file:///E:/projects/vss/server/docs/adr/0002-crdt-structural-convergence.md).

## Collaboration protocol

- **Message framing**:
  - WebSocket JSON framing via `collab.Message`.
  - Message types: `welcome`, `join`, `leave`, `op`, `values`, `sync_request`, `sync_response`, `ping`, `pong`, `error`.
- **Op-then-values ordering on the same seq**:
  - When an op is processed in `Room.ApplyOp` (`Hub.Append`), the hub assigns a monotonic sequence number `r.lastSeq++`.
  - The hub emits TWO frames:
    1. An `op` frame (`Type: MsgOp`, carrying `Seq: op.Seq`) to all peers except the author.
    2. A `values` frame (`Type: MsgValues`, carrying `Seq: op.Seq`) to ALL peers including the author.
  - The order is strictly preserved: a peer always processes the sequenced operation before receiving the resulting computed values.
- **No-echo-to-author rule**:
  - The author of an op already has their local edit applied optimistically. Emitting the op frame back to the author causes duplicate application or cursor jumps.
  - The values frame, however, IS sent to the author so the author's client receives authoritative server-evaluated formula results.
- **RecalcSummary fields and FormulaVersion**:
  - `values` frames carry `recalc: RecalcSummary` with: `evaluated`, `totalFormulas`, `full`, `volatileCells`, `staleReads`, `changedCells`, and `formulaVersion`.
  - `formulaVersion` is pinned to package constant `sheet.FormulaVersion = "sheet/1"` (`internal/sheet/engine.go:11`).
- **WS handshake authentication**:
  - Handshake requires JWT in query parameter: `GET /ws?doc_id=<doc>&token=<jwt>`.
  - Handshake implementation: [internal/collab/ws.go:178](file:///E:/projects/vss/server/internal/collab/ws.go#L178) (`token := r.URL.Query().Get("token")`).
  - Unauthenticated sockets are rejected with close frame 4001 (`websocket.FormatCloseMessage(4001, "unauthorized")`).
  - Document ID is derived from or validated against the JWT claims (`claims.HasDocPermission(docID)`).
  - Actor ID is derived strictly from authenticated user ID (`user.ID` from `claims.Subject`), closing actor-spoofing vectors.
  - Connection sends `welcome` frame carrying `user_id` and `actor_id` upon successful upgrade.
- **Writer document collaboration**:
  - Writer rooms (`Kind: "writer"`) stream operations only (`writer.insert_text`, `writer.delete_range`).
  - Writer note: op stream only, no values stream. Unlike spreadsheet rooms, there is no formula calculation and no `values` frame emitted.
  - Sockets in Writer rooms receive sequenced `op` frames only.
  - Tombstone pruning and character formatting are explicitly deferred / out of scope in Phase 13.

## Limitations

| # | Description | Status | Citation (file:line) | Verification Test |
|---|-------------|--------|----------------------|-------------------|
| 1 | Structural reference rewriting on axis insert/delete | CLOSED | [internal/sheet/rewrite.go:82](file:///E:/projects/vss/server/internal/sheet/rewrite.go#L82) (`func RewriteFormula`) | [internal/sheet/rewrite_test.go:7](file:///E:/projects/vss/server/internal/sheet/rewrite_test.go#L7) & [internal/crdt/grid_test.go:182](file:///E:/projects/vss/server/internal/crdt/grid_test.go#L182) |
| 2 | Formula clearing on explicit raw write or empty payload | CLOSED | [internal/crdt/op.go:31](file:///E:/projects/vss/server/internal/crdt/op.go#L31) (`Formula *string`) | [internal/crdt/grid_test.go:216](file:///E:/projects/vss/server/internal/crdt/grid_test.go#L216) (`TestGrid_FormulaClearing`) |
| 3 | Change detection via incremental `ChangeFeed` | CLOSED | [internal/sheet/engine.go:63](file:///E:/projects/vss/server/internal/sheet/engine.go#L63) (`type ChangeFeed struct`) | [internal/sheet/engine_test.go:16](file:///E:/projects/vss/server/internal/sheet/engine_test.go#L16) (`TestEngine_IncrementalRecalc`) |
| 4 | Concurrent structural edits do NOT converge | CLOSED | [internal/crdt/grid.go:275](file:///E:/projects/vss/server/internal/crdt/grid.go#L275) (`case OpGridInsertAxis`) & [internal/crdt/pos.go:243](file:///E:/projects/vss/server/internal/crdt/pos.go#L243) (`func BetweenWithActor`) | [internal/crdt/structural_convergence_test.go:13](file:///E:/projects/vss/server/internal/crdt/structural_convergence_test.go#L13) (`TestStructuralConvergence`) & [internal/crdt/grid_test.go:253](file:///E:/projects/vss/server/internal/crdt/grid_test.go#L253) (`TestConcurrentRowInsertBothOrders`); see [docs/adr/0002-crdt-structural-convergence.md](file:///E:/projects/vss/server/docs/adr/0002-crdt-structural-convergence.md) |
| 5 | WS handshake authentication and actor ID binding | CLOSED | [internal/collab/ws.go:178](file:///E:/projects/vss/server/internal/collab/ws.go#L178) (`token := r.URL.Query().Get("token")`) | [internal/collab/ws_auth_test.go:48](file:///E:/projects/vss/server/internal/collab/ws_auth_test.go#L48) & [internal/api/server_smoke_test.go:24](file:///E:/projects/vss/server/internal/api/server_smoke_test.go#L24) |
| 6 | Cross-sheet referencing unsupported (single-sheet only) | OPEN | [internal/sheet/address.go:15](file:///E:/projects/vss/server/internal/sheet/address.go#L15) (`type CellAddr struct`) | Limitation in address model: coordinates lack sheet/tab identifier |
| 7 | Unbounded in-memory collaboration operation log | OPEN | [internal/collab/hub.go:20](file:///E:/projects/vss/server/internal/collab/hub.go#L20) (`opLog []crdt.Op`) | Missing snapshot compaction / op pruning checkpointing |
| 8 | Metrics endpoint not implemented | OPEN | [cmd/vss-server/main.go:34](file:///E:/projects/vss/server/cmd/vss-server/main.go#L34) (`no metrics endpoint implemented`) | Flag `-metrics` removed; see `cmd/vss-server/main.go` for where it would be wired |
| 9 | Hub room eviction on document deletion | OPEN | [internal/api/handlers_docs.go:124](file:///E:/projects/vss/server/internal/api/handlers_docs.go#L124) (`func handleDeleteDoc`) | `DELETE /api/docs/{id}` removes metadata and disk snapshot, but does not evict active in-memory Room from Hub |
| 10 | Document versioning, sharing, and role models | OPEN | [internal/api/docs.go:21](file:///E:/projects/vss/server/internal/api/docs.go#L21) (`type DocMetadata struct`) | Document model lacks revision history, fine-grained access control lists, and multi-user sharing permissions |
| 11 | Writer tombstone pruning | OPEN | [internal/writer/doc.go:34](file:///E:/projects/vss/server/internal/writer/doc.go#L34) (`tombstones map[string]bool`) | Tombstones retained in memory and snapshots for convergence; pruning deferred |
| 12 | Writer character formatting and rich text | OPEN | [internal/writer/doc.go:12](file:///E:/projects/vss/server/internal/writer/doc.go#L12) (`type Char struct`) | Plain text only; rich text formatting unsupported |

