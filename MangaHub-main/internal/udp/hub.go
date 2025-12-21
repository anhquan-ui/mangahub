package udp

import (
	"encoding/json"
	"log"
	"net"
	"sync"
	"time"

	"mangahub/internal/shared"
	"mangahub/pkg/models"
)

type ClientAddr struct {
	Addr     *net.UDPAddr
	LastSeen time.Time
}

type Hub struct {
	clients   map[string]*ClientAddr
	broadcast chan []byte
	Register  chan *ClientAddr
	mu        sync.RWMutex
}

var GlobalHub = &Hub{
	clients:   make(map[string]*ClientAddr),
	broadcast: make(chan []byte),
	Register:  make(chan *ClientAddr),
}

func (h *Hub) Run() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case client := <-h.Register:
			key := client.Addr.String()
			h.mu.Lock()
			if old, exists := h.clients[key]; exists {
				old.LastSeen = time.Now()
			} else {
				h.clients[key] = client
				log.Printf("UDP CLIENT SUBSCRIBED: %s (Total: %d)", key, len(h.clients))
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.RLock()
			for key, client := range h.clients {
				_, err := udpConn.WriteToUDP(message, client.Addr)
				if err != nil {
					log.Printf("UDP send failed to %s: %v", key, err)
				} else {
					client.LastSeen = time.Now()
				}
			}
			h.mu.RUnlock()

		case <-ticker.C:
			h.mu.Lock()
			now := time.Now()
			for key, client := range h.clients {
				if now.Sub(client.LastSeen) > 30*time.Second {
					delete(h.clients, key)
					log.Printf("UDP CLIENT TIMED OUT: %s (Remaining: %d)", key, len(h.clients))
				}
			}
			h.mu.Unlock()
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
		Type:           "progress_update",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Println("UDP marshal error:", err)
		return
	}

	h.broadcast <- append(data, '\n')
}

func (h *Hub) GetClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
