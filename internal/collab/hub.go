package collab

import (
	"fmt"
	"sync"
	"time"

	"github.com/vss-suite/vss/server/internal/crdt"
	"github.com/vss-suite/vss/server/internal/identity"
	"github.com/vss-suite/vss/server/internal/sheet"
	"github.com/vss-suite/vss/server/internal/writer"
)

// Room manages a single document's collaboration session, CRDT Grid, or Writer document.
type Room struct {
	mu      sync.Mutex
	DocID   string
	Kind    string // "grid" or "writer"
	Grid    *crdt.Grid
	Engine  *sheet.Engine
	Writer  *writer.Doc
	lastSeq int64
	opLog   []crdt.Op
	clients map[*Client]bool
}

// NewRoom constructs an active collaboration room for a document (defaults to "grid").
func NewRoom(docID string) *Room {
	return NewRoomWithKind(docID, "grid")
}

// NewRoomWithKind constructs an active collaboration room of the specified kind ("grid" or "writer").
func NewRoomWithKind(docID, kind string) *Room {
	if kind == "writer" {
		return &Room{
			DocID:   docID,
			Kind:    "writer",
			Writer:  writer.NewDoc(docID),
			clients: make(map[*Client]bool),
		}
	}
	grid := crdt.NewGrid(docID)
	engine := sheet.NewEngine(grid)
	return &Room{
		DocID:   docID,
		Kind:    "grid",
		Grid:    grid,
		Engine:  engine,
		clients: make(map[*Client]bool),
	}
}

// AddClient joins a client to this room.
func (r *Room) AddClient(c *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[c] = true
	c.DocID = r.DocID
}

// RemoveClient removes a client from this room.
func (r *Room) RemoveClient(c *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clients, c)
}

// ClientCount returns the number of active clients in this room.
func (r *Room) ClientCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.clients)
}

// ApplyOp processes an incoming operation, runs recalculation, and broadcasts op and values.
func (r *Room) ApplyOp(op crdt.Op, sender *Client) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Assign monotonic sequence number
	r.lastSeq++
	op.Seq = r.lastSeq

	if r.Kind == "writer" {
		if err := r.Writer.Apply(op); err != nil {
			return fmt.Errorf("apply op to writer: %w", err)
		}
		r.opLog = append(r.opLog, op)

		// Broadcast the sequenced op to all connected peers EXCEPT the author
		opMsg := Message{
			Type:    MsgOp,
			DocID:   r.DocID,
			ActorID: op.ActorID,
			Seq:     op.Seq,
			Op:      &op,
		}
		for client := range r.clients {
			if client != sender {
				client.SendMsg(opMsg)
			}
		}
		// Writer note: op stream only, no values stream
		return nil
	}

	feed, err := r.Grid.Apply(op)
	if err != nil {
		return fmt.Errorf("apply op to grid: %w", err)
	}

	r.opLog = append(r.opLog, op)

	// Broadcast the sequenced op to all connected peers EXCEPT the author
	opMsg := Message{
		Type:    MsgOp,
		DocID:   r.DocID,
		ActorID: op.ActorID,
		Seq:     op.Seq,
		Op:      &op,
	}
	for client := range r.clients {
		if client != sender {
			client.SendMsg(opMsg)
		}
	}

	// Recalculate and broadcast values frame to ALL peers INCLUDING author
	changeFeed := sheet.ChangeFeed{}
	if feed != nil {
		changeFeed = *feed
	}

	stats, err := r.Engine.Recalculate(changeFeed)
	if err != nil {
		return fmt.Errorf("engine recalculate: %w", err)
	}

	changedSet := make(map[sheet.CellAddr]bool)
	for _, c := range changeFeed.Cells {
		changedSet[c] = true
	}
	for _, c := range stats.Changed {
		changedSet[c] = true
	}

	var values []CellValueItem
	for addr := range changedSet {
		if cv, ok := r.Grid.GetCell(addr); ok {
			values = append(values, CellValueItem{
				Row:   addr.Row,
				Col:   addr.Col,
				Value: cv,
			})
		}
	}

	evalCount := stats.Evaluated
	for _, c := range stats.Changed {
		evalCount += len(r.Engine.Precedents(c))
	}

	recalc := &RecalcSummary{
		Evaluated:      evalCount,
		TotalFormulas:  stats.TotalFormulas,
		Full:           stats.Full,
		VolatileCells:  stats.VolatileCells,
		StaleReads:     stats.StaleReads,
		ChangedCells:   len(changedSet),
		FormulaVersion: sheet.FormulaVersion,
	}

	valuesMsg := Message{
		Type:   MsgValues,
		DocID:  r.DocID,
		Seq:    op.Seq,
		Values: values,
		Recalc: recalc,
	}
	r.broadcastLocked(valuesMsg)

	return nil
}

// SyncClient sends missing operations since lastSeq to the client.
func (r *Room) SyncClient(c *Client, lastSeq int64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var missing []crdt.Op
	for _, op := range r.opLog {
		if op.Seq > lastSeq {
			missing = append(missing, op)
		}
	}

	c.SendMsg(Message{
		Type:    MsgSyncResponse,
		DocID:   r.DocID,
		LastSeq: r.lastSeq,
		Ops:     missing,
	})
}

func (r *Room) broadcastLocked(msg Message) {
	for client := range r.clients {
		client.SendMsg(msg)
	}
}

// HubOptions provides configuration options for Hub.
type HubOptions struct {
	Auth         identity.TokenVerifier
	Clock        func() time.Time
	RNG          func() float64
	KindResolver func(docID string) (string, error)
}

// Hub manages active collaboration rooms.
type Hub struct {
	mu           sync.RWMutex
	rooms        map[string]*Room
	Auth         identity.TokenVerifier
	clock        func() time.Time
	rng          func() float64
	KindResolver func(docID string) (string, error)
}

// NewHub initializes a Hub with an optional TokenVerifier.
func NewHub(auth ...identity.TokenVerifier) *Hub {
	var v identity.TokenVerifier
	if len(auth) > 0 {
		v = auth[0]
	}
	return &Hub{
		rooms: make(map[string]*Room),
		Auth:  v,
	}
}

// NewHubWithOptions initializes a Hub with specific options (injected Clock, RNG, Auth, KindResolver).
func NewHubWithOptions(opts HubOptions) *Hub {
	return &Hub{
		rooms:        make(map[string]*Room),
		Auth:         opts.Auth,
		clock:        opts.Clock,
		rng:          opts.RNG,
		KindResolver: opts.KindResolver,
	}
}

// GetOrCreateRoom returns the room for docID, creating it if it does not exist.
func (h *Hub) GetOrCreateRoom(docID string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()

	room, ok := h.rooms[docID]
	if !ok {
		kind := "grid"
		if h.KindResolver != nil {
			if k, err := h.KindResolver(docID); err == nil && k != "" {
				kind = k
			}
		}
		room = NewRoomWithKind(docID, kind)
		if room.Engine != nil {
			if h.clock != nil {
				room.Engine.Clock = h.clock
			}
			if h.rng != nil {
				room.Engine.RNG = h.rng
			}
		}
		h.rooms[docID] = room
	}
	return room
}

// Append applies an op to the specified document room.
func (h *Hub) Append(docID string, op crdt.Op, sender *Client) error {
	room := h.GetOrCreateRoom(docID)
	return room.ApplyOp(op, sender)
}

// RemoveRoom cleans up an empty room.
func (h *Hub) RemoveRoom(docID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.rooms, docID)
}
