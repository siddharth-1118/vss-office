package collab

import (
	"github.com/vss-suite/vss/server/internal/crdt"
	"github.com/vss-suite/vss/server/internal/sheet"
)

// Collaboration message types
const (
	MsgWelcome      = "welcome"
	MsgJoin         = "join"
	MsgLeave        = "leave"
	MsgOp           = "op"
	MsgValues       = "values"
	MsgSyncRequest  = "sync_request"
	MsgSyncResponse = "sync_response"
	MsgPing         = "ping"
	MsgPong         = "pong"
	MsgError        = "error"
)

// RecalcSummary reports engine metrics and formula evaluation stats.
type RecalcSummary struct {
	Evaluated      int    `json:"evaluated"`
	TotalFormulas  int    `json:"totalFormulas"`
	Full           bool   `json:"full"`
	VolatileCells  int    `json:"volatileCells"`
	StaleReads     int    `json:"staleReads"`
	ChangedCells   int    `json:"changedCells"`
	FormulaVersion string `json:"formulaVersion"`
}

// Message is the standard WebSocket protocol framing for collaboration.
type Message struct {
	Type      string          `json:"type"`
	DocID     string          `json:"doc_id,omitempty"`
	ActorID   string          `json:"actor_id,omitempty"`
	UserID    string          `json:"user_id,omitempty"`
	Seq       int64           `json:"seq,omitempty"`
	LastSeq   int64           `json:"last_seq,omitempty"`
	Op        *crdt.Op        `json:"op,omitempty"`
	Ops       []crdt.Op       `json:"ops,omitempty"`
	Values    []CellValueItem `json:"values,omitempty"`
	Recalc    *RecalcSummary  `json:"recalc,omitempty"`
	Error     string          `json:"error,omitempty"`
	Timestamp int64           `json:"timestamp,omitempty"`
}

// CellValueItem packages a single cell's coordinate and computed value for the values stream.
type CellValueItem struct {
	Row   int             `json:"row"`
	Col   int             `json:"col"`
	Value sheet.CellValue `json:"value"`
}
