package collab

import (
	"fmt"
	"sync"

	"github.com/vss-suite/vss/server/internal/crdt"
	"github.com/vss-suite/vss/server/internal/sheet"
)

// Room manages a single document's collaboration session, CRDT Grid, and recalculation engine.
type Room struct {
	mu      sync.Mutex
	DocID   string
	Grid    *crdt.Grid
	Engine  *sheet.Engine
	lastSeq int64
	opLog   []crdt.Op
	clients map[*Client]bool
}

// NewRoom constructs an active collaboration room for a document.
func NewRoom(docID string) *Room {
	grid := crdt.NewGrid(docID)
	engine := sheet.NewEngine(grid)
	return &Room{
		DocID:   docID,
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

	feed, err := r.Grid.Apply(op)
	if err != nil {
		return fmt.Errorf("apply op to grid: %w", err)
	}

	r.opLog = append(r.opLog, op)

	// Broadcast the sequenced op to all connected clients
	opMsg := Message{
		Type:    MsgOp,
		DocID:   r.DocID,
		ActorID: op.ActorID,
		Seq:     op.Seq,
		Op:      &op,
	}
	r.broadcastLocked(opMsg)

	// Run incremental recalculation if cells or axes changed
	if feed != nil && (len(feed.Cells) > 0 || feed.AxesChanged) {
		stats, err := r.Engine.Recalculate(*feed)
		if err == nil && len(stats.Changed) > 0 {
			var values []CellValueItem
			for _, addr := range stats.Changed {
				if cv, ok := r.Grid.GetCell(addr); ok {
					values = append(values, CellValueItem{
						Row:   addr.Row,
						Col:   addr.Col,
						Value: cv,
					})
				}
			}
			if len(values) > 0 {
				valuesMsg := Message{
					Type:   MsgValues,
					DocID:  r.DocID,
					Values: values,
				}
				r.broadcastLocked(valuesMsg)
			}
		}
	}

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

// Hub manages active collaboration rooms.
type Hub struct {
	mu    sync.RWMutex
	rooms map[string]*Room
}

// NewHub initializes a Hub.
func NewHub() *Hub {
	return &Hub{
		rooms: make(map[string]*Room),
	}
}

// GetOrCreateRoom returns the room for docID, creating it if it does not exist.
func (h *Hub) GetOrCreateRoom(docID string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()

	room, ok := h.rooms[docID]
	if !ok {
		room = NewRoom(docID)
		h.rooms[docID] = room
	}
	return room
}

// RemoveRoom cleans up an empty room.
func (h *Hub) RemoveRoom(docID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.rooms, docID)
}
