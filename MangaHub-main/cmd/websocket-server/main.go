package main

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"
	"encoding/json"

	"mangahub/internal/websocket"

	"github.com/gin-gonic/gin"
	ws "github.com/gorilla/websocket"
)

var upgrader = ws.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

var hub *websocket.Hub

func main() {
	// Create and start hub
	hub = websocket.NewHub()
	go hub.Run()

	// Create Gin router
	router := gin.Default()

	// CORS middleware
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// Serve chat HTML page
	router.GET("/", func(c *gin.Context) {
		c.File(filepath.Join("web", "chat.html"))
	})

	// WebSocket endpoint
	router.GET("/ws", handleWebSocket)

	// Stats endpoint
	router.GET("/stats", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"online_users": hub.GetClientCount(),
			"timestamp":    time.Now().Format("15:04:05"),
		})
	})

	fmt.Println("🚀 WebSocket Chat Server started on :9093")
	fmt.Println("📱 Open your browser: http://localhost:9093")

	if err := router.Run(":9093"); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}

func handleWebSocket(c *gin.Context) {
	username := c.Query("username")
	if username == "" {
		c.JSON(400, gin.H{"error": "username required"})
		return
	}

	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}

	// Create new client
	client := &websocket.Client{
		Hub:      hub,
		Conn:     conn,
		Send:     make(chan []byte, 256),
		Username: username,
	}

	// Register client
	hub.Register <- client

	// Send message history to new user
	history := hub.GetMessageHistory()
	for _, msg := range history {
		data, _ := json.Marshal(msg)
		client.Send <- data
	}

	// Send join message to all
	hub.BroadcastMessage(websocket.Message{
		Type:     "system",
		Text:     fmt.Sprintf("%s joined the chat", username),
		Time:     time.Now().Format("15:04:05"),
	})

	// Start goroutines for reading and writing
	go client.WritePump()
	
	// ReadPump will handle unregister in its defer
	go func() {
		client.ReadPump()
		// Send leave message after client disconnects
		hub.BroadcastMessage(websocket.Message{
			Type:     "system",
			Text:     fmt.Sprintf("%s left the chat", username),
			Time:     time.Now().Format("15:04:05"),
		})
	}()
}