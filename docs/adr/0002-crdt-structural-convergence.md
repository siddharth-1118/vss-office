# ADR 0002: CRDT Structural Convergence for Concurrent Axis Edits

## Status
Proposed

## Context
In VSS Grid, spreadsheet data is stored in a coordinate-backed CRDT (`crdt.Grid`) where cells are indexed by integer `(Row, Col)` coordinates (`internal/sheet/address.go:15`).
While scalar cell updates converge via Last-Write-Wins (LWW) conflict resolution using monotonic timestamps and actor IDs (`internal/crdt/grid.go:73-82`), structural axis operations (`OpGridInsertAxis` and `OpGridDeleteAxis`) currently rely on naive integer coordinate shifting (`internal/crdt/grid.go:138, 151, 190, 207`).

When two replicas concurrently insert rows or columns at the same index, coordinate shifting violates **intention preservation**:
- An edit's intention is to insert a new row relative to existing semantic content, not relative to an ephemeral integer offset.
- Shifting coordinates purely based on message arrival order results in non-commutative operations.

This defect is proven by the failing test `TestStructuralConvergence` in [internal/crdt/structural_convergence_test.go](file:///E:/projects/vss/server/internal/crdt/structural_convergence_test.go#L14):

```
=== RUN   TestStructuralConvergence
    structural_convergence_test.go:137: CRDT structural divergence detected:
          replica A has [[r0_original r1_original rB_inserted rA_inserted r2_original]]
          replica B has [[r0_original r1_original rA_inserted rB_inserted r2_original]]
        See docs/adr/0002-crdt-structural-convergence.md
--- FAIL: TestStructuralConvergence (0.00s)
FAIL
```

Replica A observed local insertion `rA` first, then shifted it upon receiving `rB`, resulting in row order `[r0, r1, rB, rA, r2]`.
Replica B observed local insertion `rB` first, then shifted it upon receiving `rA`, resulting in row order `[r0, r1, rA, rB, r2]`.
The two replicas reach divergent states despite having processed the exact same set of operations.

## Options

### (a) Fractional-Index Positions per Row and per Column
- **Description**: Assign each row and column an orderable position identifier (e.g. fractional index strings such as Fractional Indexing / LSEQ / Logoot). Inserting an axis between position `P_i` and `P_{i+1}` allocates an intermediate key `P_{new}` with actor ID tie-breaking.
- **Latency**: Zero roundtrip delay. Purely local and immediate.
- **Storage**: Low overhead. Positions are compact strings (e.g. `8-16` bytes per axis).
- **Complexity**: Moderate. Requires fractional indexing key generation logic that avoids key length runaway under pathological sequential insertions.
- **Wire Compatibility**: High. Payload replaces integer `Index` with boundary position keys `(LeftKey, RightKey)` or maps logical index to fractional key on the server.
- **Migration**: Straightforward. Existing integer rows `0, 1, 2, ...` map directly to initial baseline fractional keys (`"a0"`, `"a1"`, `"a2"`).
- **Offline Behavior**: Fully local-first. Offline clients can insert axes freely and merge deterministically when reconnecting.

### (b) Per-Row and Per-Column Stable UUIDs with Explicit Ordering CRDT (RGA / Fugue / YATA)
- **Description**: Treat row and column sequences as a sequence CRDT (such as Replicated Growable Array or Fugue). Each axis has an immutable UUID and an explicit tree/linked-list relation to its left/right neighbor. Cells are stored under `(row_uuid, col_uuid)`.
- **Latency**: Zero roundtrip delay. Commutative and local-first.
- **Storage**: Moderate-to-high. Requires retaining tombstones for deleted rows/columns to maintain causal links unless garbage collection is coordinated.
- **Complexity**: High. Managing sequence CRDT interleaving, tree balances, and tombstone pruning is non-trivial.
- **Wire Compatibility**: Breaking change to grid addressing. Cell coordinates on the wire become UUID pairs or require translation tables.
- **Migration**: Requires assigning synthetic UUIDs to all existing cells and axes during snapshot conversion.
- **Offline Behavior**: Excellent. Guarantees maximal intention preservation across long offline partitions.

### (c) Server-Serialized Structural Operations with Optimistic Rollback
- **Description**: Maintain integer coordinates in `crdt.Grid`. Require all structural operations (`insert_axis`, `delete_axis`) to be sequenced and validated exclusively by the central collaboration server. If an offline client produces a structural edit against an outdated sequence number, the server rejects it or transforms it via Operational Transformation (OT).
- **Latency**: High. Structural edits cannot be finalized optimistically without risk of rollback.
- **Storage**: Zero added storage overhead.
- **Complexity**: Low in CRDT data structures, but significantly complicates client state machines (requires undo/redo and rollback on server rejection).
- **Wire Compatibility**: 100% compatible with existing `GridInsertAxisPayload` and `GridDeleteAxisPayload`.
- **Migration**: Zero migration required.
- **Offline Behavior**: Poor. Offline structural editing is either prohibited or prone to conflict rejections.

## Trade-offs

| Criterion | (a) Fractional Indexing | (b) Sequence CRDT (RGA/Fugue) | (c) Server-Serialized OT |
|---|---|---|---|
| **Latency** | Immediate (local) | Immediate (local) | High (requires server ACK) |
| **Storage Overhead** | Negligible (~16B per axis) | Moderate (tombstones) | None |
| **Implementation Complexity** | Moderate (~200 LOC generator) | High (~800-1500 LOC) | Moderate in client, low in server |
| **Wire Compatibility** | Minor extension | Major refactor (UUID addresses) | 100% backward compatible |
| **Existing Doc Migration** | Trivial linear assignment | Synthetic UUID generation | None |
| **Offline Editing** | Supported, fully convergent | Supported, fully convergent | Broken or subject to rollbacks |

## Recommendation

**Option (a): Fractional-Index Positions per Row and Column.**

### Reasoning:
1. **Convergence without Tombstones**: Fractional indexing guarantees mathematical convergence across any number of replicas without introducing tombstone memory leaks or complex garbage collection protocols.
2. **Local-First & Offline First**: Preserves the core architectural tenet of VSS (CRDT-native collaboration that merges locally first, then syncs).
3. **Storage & Formula Alignment**: Cells continue to be addressable via fast lookups, and formula reference rewriting (`sheet.RewriteFormula`) maps naturally to shifted order ranks.

### What would change the recommendation:
If the VSS document engine transitions its core cell data structures into a unified WASM-compiled sequence CRDT engine (e.g., Rust-based Yrs or Automerge as referenced in the suite architectural roadmap), **Option (b)** would become the preferred path to share the underlying RGA/Fugue implementation between Grid, Writer, and Present.

## Migration plan
1. Introduce `Position` type (orderable byte string) in `internal/crdt`.
2. Existing canonical snapshots (`CanonicalGridSnapshot`) that lack position metadata are automatically assigned canonical baseline positions: `pos(i) = GenerateKey(i)` on import.
3. Wire payloads (`GridInsertAxisPayload`) will accept optional `AfterPosition` and `BeforePosition` strings. When omitted, server maps integer `Index` to existing fractional key span.
4. Formula AST reference rewriting will operate on order ranks derived from sorted fractional positions.

## Consequences
- **What closes**: Elimination of non-deterministic state divergence under concurrent row/column additions.
- **What remains**: A future session must implement the fractional indexing generator, update `Apply(OpGridInsertAxis)`, and verify that `TestStructuralConvergence` passes.
- **Action items for future implementation session**:
  1. Implement `internal/crdt/fractional_index.go` with jittered midpoint generation and actor tie-breaking.
  2. Update `Grid.Apply` for `OpGridInsertAxis` and `OpGridDeleteAxis` to maintain sorted axis positions.
  3. Remove `//go:build convergence` from `structural_convergence_test.go` and ensure it runs as part of the default test gate.
