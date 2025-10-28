package service

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	conn   *websocket.Conn
	send   chan []byte
	pageID string
}

type Hub struct {
	clients    map[string]map[*Client]bool
	register   chan *Client
	unregister chan *Client
	broadcast  chan []byte
	mu         sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[string]map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan []byte, 256),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			if _, ok := h.clients[c.pageID]; !ok {
				h.clients[c.pageID] = make(map[*Client]bool)
			}
			h.clients[c.pageID][c] = true
			h.mu.Unlock()

			// increment in redis and publish update
			newCnt, _ := IncrCount(c.pageID)
			publishPresence(c.pageID, newCnt)

		case c := <-h.unregister:
			h.mu.Lock()
			if conns, ok := h.clients[c.pageID]; ok {
				if _, exists := conns[c]; exists {
					delete(conns, c)
					close(c.send)
				}
				if len(conns) == 0 {
					delete(h.clients, c.pageID)
				}
			}
			h.mu.Unlock()

			newCnt, _ := DecrCount(c.pageID)
			if newCnt < 0 {
				newCnt = 0
			}
			publishPresence(c.pageID, newCnt)

		case msg := <-h.broadcast:
			// message from redis pubsub - it's JSON {"page":"id","count":n}
			var m struct {
				Page  string `json:"page"`
				Count int64  `json:"count"`
			}
			if err := json.Unmarshal(msg, &m); err != nil {
				continue
			}
			h.mu.RLock()
			conns := h.clients[m.Page]
			for client := range conns {
				select {
				case client.send <- msg:
				default:
					// slow client, force close
					close(client.send)
					delete(conns, client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // adjust in prod
}

func ServeWs(h *Hub, w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pageID := q.Get("page_id")
	if pageID == "" {
		pageID = "default"
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &Client{conn: conn, send: make(chan []byte, 256), pageID: pageID}
	h.register <- client

	// start writer
	go client.writePump()
	// start reader
	client.readPump(h)
}

// readPump just listens; client can send pings or actions if needed
func (c *Client) readPump(h *Hub) {
	defer func() {
		h.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			break
		}
		// we don't expect client messages for now
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// hub closed channel
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)
			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			// send ping
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
