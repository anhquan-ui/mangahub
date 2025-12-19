package tcp

import (
	"encoding/json"
	"log"
	"sync"
	"time"
	"net"

	"mangahub/pkg/models"
	"mangahub/internal/shared"
)

type Client struct {
	Conn   net.Conn
	UserID string
	Send   chan []byte
}

type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	Register   chan *Client
	Unregister chan *Client
	mu         sync.RWMutex
}

var GlobalHub = &Hub{
	clients:    make(map[*Client]bool),
	broadcast:  make(chan []byte),
	Register:   make(chan *Client),
	Unregister: make(chan *Client),
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.Register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Printf("Client connected: %s (UserID: %s)", client.Conn.RemoteAddr(), client.UserID)

		case client := <-h.Unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.Send)
			}
			h.mu.Unlock()
			log.Printf("Client disconnected: %s", client.Conn.RemoteAddr())

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.Send <- message:
				default:
					close(client.Send)
					delete(h.clients, client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *Hub) BroadcastProgress(update models.UserProgress, username, mangaTitle string) {
	msg := shared.ProgressUpdate{
		UserID:         update.UserID,
		Username:       username,
		MangaID:        update.MangaID,
		MangaTitle:     mangaTitle,
		CurrentChapter: update.CurrentChapter,
		Status:         update.Status,
		Timestamp:      time.Now().Unix(),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Println("Error marshaling progress update:", err)
		return
	}

	h.broadcast <- data
}