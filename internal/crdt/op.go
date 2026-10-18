package crdt

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	OpGridSetCell       = "grid.set_cell"
	OpGridInsertAxis    = "grid.insert_axis"
	OpGridDeleteAxis    = "grid.delete_axis"
	OpWriterInsertText  = "writer.insert_text"
	OpWriterDeleteRange = "writer.delete_range"
)

type Op struct {
	ID        string          `json:"id"`
	DocID     string          `json:"doc_id"`
	Seq       int64           `json:"seq"`
	ActorID   string          `json:"actor_id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp int64           `json:"timestamp"` // Unix milliseconds
}

type GridSetCellPayload struct {
	Row     int            `json:"row"`
	Col     int            `json:"col"`
	RowPos  Pos            `json:"row_pos,omitempty"`
	ColPos  Pos            `json:"col_pos,omitempty"`
	Raw     string         `json:"raw,omitempty"`
	Formula *string        `json:"formula,omitempty"`
	Style   map[string]any `json:"style,omitempty"`
}

type GridInsertAxisPayload struct {
	Axis           string `json:"axis"` // "row" or "col"
	Index          int    `json:"index"`
	Count          int    `json:"count"`
	Pos            Pos    `json:"pos,omitempty"`
	AfterPosition  Pos    `json:"after_pos,omitempty"`
	BeforePosition Pos    `json:"before_pos,omitempty"`
}

type GridDeleteAxisPayload struct {
	Axis  string `json:"axis"` // "row" or "col"
	Index int    `json:"index"`
	Count int    `json:"count"`
	Pos   Pos    `json:"pos,omitempty"`
}

type WriterInsertTextPayload struct {
	Pos       Pos    `json:"pos,omitempty"`
	After     Pos    `json:"after,omitempty"`
	Before    Pos    `json:"before,omitempty"`
	Text      string `json:"text"`
	Positions []Pos  `json:"positions,omitempty"`
}

type WriterDeleteRangePayload struct {
	From Pos      `json:"from,omitempty"`
	To   Pos      `json:"to,omitempty"`
	IDs  []string `json:"ids,omitempty"`
}

// NewOp creates an Op, accepting any typed struct payload and marshaling it to JSON internally.
func NewOp(docID, actorID, opType string, payload any) (Op, error) {
	var rawPayload json.RawMessage
	switch p := payload.(type) {
	case json.RawMessage:
		rawPayload = p
	case []byte:
		rawPayload = json.RawMessage(p)
	default:
		data, err := json.Marshal(payload)
		if err != nil {
			return Op{}, fmt.Errorf("marshal payload: %w", err)
		}
		rawPayload = data
	}

	return Op{
		ID:        uuid.NewString(),
		DocID:     docID,
		ActorID:   actorID,
		Type:      opType,
		Payload:   rawPayload,
		Timestamp: time.Now().UnixMilli(),
	}, nil
}

// UnmarshalPayload deserializes the Op payload into the destination struct.
func (op Op) UnmarshalPayload(dest any) error {
	return json.Unmarshal(op.Payload, dest)
}
