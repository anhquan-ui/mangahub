package websocket

import (
	"encoding/json"
	"log"
	"sync"
)

// Message represents a chat message
type Message struct {
	Type     string `json:"type"`     // "join", "leave", "chat", "typing"
	Username string `json:"username"`
	Text     string `json:"text"`
	Time     string `json:"time"`
}

// Hub maintains the set of active clients and broadcasts messages
type Hub struct {
	// Registered clients
	clients map[*Client]bool

	// Inbound messages from clients
	Broadcast chan []byte

	// Register requests from clients
	Register chan *Client

	// Unregister requests from clients
	Unregister chan *Client

	// Message history (last 50 messages)
	messageHistory []Message

	// Mutex for thread-safe operations
	mu sync.RWMutex
}

// NewHub creates a new Hub
func NewHub() *Hub {
	return &Hub{
		clients:        make(map[*Client]bool),
		Broadcast:      make(chan []byte),
		Register:       make(chan *Client),
		Unregister:     make(chan *Client),
		messageHistory: make([]Message, 0, 50),
	}
}

// Run starts the hub's main loop
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.Register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Printf("Client registered: %s (Total: %d)", client.Username, len(h.clients))

		case client := <-h.Unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.Send)
				log.Printf("Client unregistered: %s (Total: %d)", client.Username, len(h.clients))
			}
			h.mu.Unlock()

		case message := <-h.Broadcast:
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

// BroadcastMessage sends a message to all connected clients
func (h *Hub) BroadcastMessage(msg Message) {
	// Add to history only if it's chat or system message (not typing)
	if msg.Type == "chat" || msg.Type == "system" {
		h.mu.Lock()
		h.messageHistory = append(h.messageHistory, msg)
		// Keep only last 50 messages
		if len(h.messageHistory) > 50 {
			h.messageHistory = h.messageHistory[1:]
		}
		h.mu.Unlock()
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling message: %v", err)
		return
	}
	h.Broadcast <- data
}

// GetMessageHistory returns the message history
func (h *Hub) GetMessageHistory() []Message {
	h.mu.RLock()
	defer h.mu.RUnlock()
	history := make([]Message, len(h.messageHistory))
	copy(history, h.messageHistory)
	return history
}

// GetClientCount returns the number of connected clients
func (h *Hub) GetClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}