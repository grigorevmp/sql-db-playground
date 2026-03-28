package hub

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type Client struct {
	hub       *Hub
	conn      *websocket.Conn
	send      chan []byte
	UserID    string
	SessionID string
}

type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}
}

func New() *Hub {
	return &Hub{clients: make(map[*Client]struct{})}
}

func (h *Hub) register(c *Client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) unregister(c *Client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}

// BroadcastAll sends msg to every connected client.
func (h *Hub) BroadcastAll(msg interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("hub broadcast marshal error: %v", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- data:
		default:
			// slow client — drop
		}
	}
}

// SendToUser sends msg only to clients with matching userID.
func (h *Hub) SendToUser(userID string, msg interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if c.UserID == userID {
			select {
			case c.send <- data:
			default:
			}
		}
	}
}

// ConnectedUserIDs returns a slice of all currently connected user IDs.
func (h *Hub) ConnectedUserIDs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	seen := make(map[string]struct{})
	var ids []string
	for c := range h.clients {
		if _, ok := seen[c.UserID]; !ok {
			seen[c.UserID] = struct{}{}
			ids = append(ids, c.UserID)
		}
	}
	return ids
}

// ServeWS upgrades the HTTP connection to WebSocket and starts read/write pumps.
// The caller must set client.UserID and client.SessionID before calling.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, userID, sessionID string, initialMsg interface{}) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}

	client := &Client{
		hub:       h,
		conn:      conn,
		send:      make(chan []byte, 64),
		UserID:    userID,
		SessionID: sessionID,
	}
	h.register(client)

	// send initial state
	if initialMsg != nil {
		data, _ := json.Marshal(initialMsg)
		client.send <- data
	}

	go client.writePump()
	go client.readPump()
}

type wsEnvelope struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

func (c *Client) writePump() {
	defer func() {
		c.conn.Close()
		c.hub.unregister(c)
	}()
	for msg := range c.send {
		env := wsEnvelope{Event: "state:update", Data: json.RawMessage(msg)}
		if err := c.conn.WriteJSON(env); err != nil {
			return
		}
	}
}

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister(c)
		c.conn.Close()
	}()
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
	}
}
