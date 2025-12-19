package main

import (
	"bufio"
	"fmt"
	"log"
	"net/http"
	"net"
	
	"mangahub/internal/shared"
	"mangahub/internal/tcp"
	"mangahub/pkg/models"

	"github.com/gin-gonic/gin"
)

func main() {
	go tcp.GlobalHub.Run()

	go startTCPListener()

	router := gin.New()
	router.POST("/internal/progress", receiveProgress)

	log.Println("TCP Server running")
	log.Println(" - TCP clients on :9090")
	log.Println(" - Internal HTTP for API on :9091/internal/progress")

	router.Run(":9091")
}

func startTCPListener() {
	listener, err := net.Listen("tcp", ":9090")
	if err != nil {
		log.Fatal("Error starting TCP listener:", err)
	}
	defer listener.Close()

	log.Println("TCP listener started on :9090")

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("Error accepting:", err)
			continue
		}
		go handleTCPConnection(conn)
	}
}

func handleTCPConnection(conn net.Conn) {
	defer conn.Close()

	fmt.Fprintf(conn, "Welcome to MangaHub Progress Sync!\nEnter your UserID: ")

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}
	userID := scanner.Text()

	client := &tcp.Client{
		Conn:   conn,
		UserID: userID,
		Send:   make(chan []byte, 256),
	}

	tcp.GlobalHub.Register <- client

	go client.WritePump()
	client.ReadPump()
}

func receiveProgress(c *gin.Context) {
	var update shared.ProgressUpdate
	if err := c.ShouldBindJSON(&update); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tcp.GlobalHub.BroadcastProgress(models.UserProgress{
		UserID:         update.UserID,
		MangaID:        update.MangaID,
		CurrentChapter: update.CurrentChapter,
		Status:         update.Status,
	}, update.Username, update.MangaTitle)

	c.JSON(http.StatusOK, gin.H{"status": "broadcasted"})
}