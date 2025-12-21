package main

import (
	"log"
	"net/http"

	"mangahub/internal/shared"
	"mangahub/internal/udp"
	"mangahub/pkg/models"

	"github.com/gin-gonic/gin"
)

func main() {
	go udp.GlobalHub.Run()

	udp.StartUDPListener(":9094")

	router := gin.New()
	router.POST("/internal/progress", receiveProgress)

	log.Println("UDP Server running")
	log.Println(" - UDP clients on :9094")
	log.Println(" - Internal HTTP for API on :9095/internal/progress")

	router.Run(":9095")
}

func receiveProgress(c *gin.Context) {
	var update shared.ProgressUpdate
	if err := c.ShouldBindJSON(&update); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	udp.GlobalHub.BroadcastProgress(models.UserProgress{
		UserID:         update.UserID,
		MangaID:        update.MangaID,
		CurrentChapter: update.CurrentChapter,
		Status:         update.Status,
	}, update.Username, update.MangaTitle)

	c.JSON(http.StatusOK, gin.H{"status": "broadcasted"})
}
