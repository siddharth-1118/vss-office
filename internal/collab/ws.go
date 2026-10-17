package collab

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512 * 1024 // 512 KB
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for dev/self-host
	},
}

// Client represents a connected collaboration peer.
type Client struct {
	Hub     *Hub
	Conn    *websocket.Conn
	Send    chan []byte
	DocID   string
	ActorID string

	mu     sync.Mutex
	closed bool
}

// NewClient initializes a new Client.
func NewClient(hub *Hub, conn *websocket.Conn, docID, actorID string) *Client {
	return &Client{
		Hub:     hub,
		Conn:    conn,
		Send:    make(chan []byte, 256),
		DocID:   docID,
		ActorID: actorID,
	}
}

// SendMsg enqueues a protocol Message to be written to the WebSocket connection.
func (c *Client) SendMsg(msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.Send <- data:
	default:
		// Drop message if buffer is completely saturated
	}
}

// Close gracefully closes the client connection.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.Send)
		if c.Conn != nil {
			c.Conn.Close()
		}
	}
}

// ReadPump handles incoming WebSocket frames from the peer.
func (c *Client) ReadPump(room *Room) {
	defer func() {
		room.RemoveClient(c)
		c.Close()
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			break
		}

		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			c.SendMsg(Message{Type: MsgError, Error: "invalid message format"})
			continue
		}

		switch msg.Type {
		case MsgJoin:
			// Document room switching after authentication is prohibited.
			if msg.DocID != "" && msg.DocID != room.DocID {
				c.SendMsg(Message{Type: MsgError, Error: "cannot switch documents on authenticated connection"})
			}
		case MsgOp:
			if msg.Op != nil {
				// Enforce server-authoritative actor ID and doc ID from authenticated connection
				msg.Op.ActorID = c.ActorID
				msg.Op.DocID = room.DocID
				if err := room.ApplyOp(*msg.Op, c); err != nil {
					c.SendMsg(Message{Type: MsgError, Error: err.Error()})
				}
			}
		case MsgSyncRequest:
			room.SyncClient(c, msg.LastSeq)
		case MsgPing:
			c.SendMsg(Message{Type: MsgPong, Timestamp: time.Now().UnixMilli()})
		case MsgLeave:
			room.RemoveClient(c)
			return
		}
	}
}

// WritePump ensures per-connection write serialization across concurrent callers.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		if c.Conn != nil {
			c.Conn.Close()
		}
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func rejectWith4001(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	closeMsg := websocket.FormatCloseMessage(4001, "unauthorized")
	_ = conn.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(time.Second))
	_ = conn.Close()
}

// ServeWS upgrades the HTTP request to WebSocket with JWT authentication and registers the client in the Hub.
func ServeWS(hub *Hub, w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" || hub.Auth == nil {
		rejectWith4001(w, r)
		return
	}

	claims, user, err := hub.Auth.VerifyClaims(token)
	if err != nil {
		rejectWith4001(w, r)
		return
	}

	// Determine document ID: verified against claims
	docID := r.URL.Query().Get("doc_id")
	if docID == "" {
		if len(claims.DocIDs) > 0 && claims.DocIDs[0] != "*" {
			docID = claims.DocIDs[0]
		} else {
			docID = "default"
		}
	} else {
		if !claims.HasDocPermission(docID) {
			rejectWith4001(w, r)
			return
		}
	}

	// ActorID is derived strictly from authenticated user.ID
	actorID := user.ID

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade error: %v", err)
		return
	}

	client := NewClient(hub, conn, docID, actorID)
	room := hub.GetOrCreateRoom(docID)
	room.AddClient(client)

	// Send welcome frame containing authenticated user identity
	client.SendMsg(Message{
		Type:    MsgWelcome,
		DocID:   docID,
		ActorID: user.ID,
		UserID:  user.ID,
	})

	go client.WritePump()
	client.ReadPump(room)
}
