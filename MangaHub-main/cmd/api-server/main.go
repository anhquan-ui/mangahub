package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"mangahub/internal/auth"
	"mangahub/internal/database"
	"mangahub/internal/shared"
	"mangahub/pkg/models"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

const tcpServerURL = "http://localhost:9091/internal/progress"
const udpServerURL = "http://localhost:9095/internal/progress"

func main() {
	if err := database.Initialize("./data/mangahub.db"); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	defer database.Close()

	if err := database.SeedManga(); err != nil {
		log.Printf("Warning: Failed to seed manga data: %v", err)
	}

	router := gin.Default()

	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://127.0.0.1:5500", "http://localhost:5500"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	router.GET("/", func(c *gin.Context) {
		c.File(filepath.Join("web", "index.html"))
	})

	public := router.Group("/")
	{
		public.POST("/auth/register", registerHandler)
		public.POST("/auth/login", loginHandler)
	}

	protected := router.Group("/")
	protected.Use(auth.Middleware())
	{
		protected.GET("/manga", getMangaHandler)
		protected.GET("/manga/:id", getMangaDetailHandler)
		protected.POST("/manga", createMangaHandler)
		protected.POST("/users/library", addToLibraryHandler)
		protected.GET("/users/library", getLibraryHandler)
		protected.PUT("/users/progress", updateProgressHandler)
	}

	log.Println("API Server starting on http://localhost:8080")
	router.Run(":8080")
}

func registerHandler(c *gin.Context) {
	var req models.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	userID := auth.GenerateID("usr")

	_, err = database.DB.Exec(`INSERT INTO users (id, username, email, password_hash) VALUES (?, ?, ?, ?)`,
		userID, req.Username, req.Email, passwordHash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	token, err := auth.GenerateToken(userID, req.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, models.LoginResponse{Token: token, Username: req.Username, UserID: userID})
}

func loginHandler(c *gin.Context) {
	var req models.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user models.User
	err := database.DB.QueryRow("SELECT id, username, password_hash FROM users WHERE username = ?", req.Username).
		Scan(&user.ID, &user.Username, &user.PasswordHash)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	token, err := auth.GenerateToken(user.ID, user.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, models.LoginResponse{Token: token, Username: user.Username, UserID: user.ID})
}

func getMangaHandler(c *gin.Context) {
	var mangaList []models.Manga
	rows, err := database.DB.Query("SELECT id, title, author, genres, status, total_chapters, description FROM manga")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch manga"})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var m models.Manga
		err := rows.Scan(&m.ID, &m.Title, &m.Author, &m.GenresString, &m.Status, &m.TotalChapters, &m.Description)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Scan error"})
			return
		}
		m.PostScan()
		mangaList = append(mangaList, m)
	}

	c.JSON(http.StatusOK, gin.H{"manga": mangaList, "count": len(mangaList)})
}

func getMangaDetailHandler(c *gin.Context) {
	id := c.Param("id")
	var m models.Manga
	err := database.DB.QueryRow("SELECT id, title, author, genres, status, total_chapters, description FROM manga WHERE id = ?", id).
		Scan(&m.ID, &m.Title, &m.Author, &m.GenresString, &m.Status, &m.TotalChapters, &m.Description)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Manga not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	m.PostScan()
	c.JSON(http.StatusOK, m)
}

func createMangaHandler(c *gin.Context) {
	var m models.Manga
	if err := c.ShouldBindJSON(&m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	m.ID = auth.GenerateID("mng")
	m.PreSave()

	_, err := database.DB.Exec(
		"INSERT INTO manga (id, title, author, genres, status, total_chapters, description) VALUES (?, ?, ?, ?, ?, ?, ?)",
		m.ID, m.Title, m.Author, m.GenresString, m.Status, m.TotalChapters, m.Description,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create manga"})
		return
	}

	c.JSON(http.StatusCreated, m)
}

func addToLibraryHandler(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		MangaID string `json:"manga_id" binding:"required"`
		Status  string `json:"status" binding:"oneof=reading completed plan_to_read"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if manga exists
	var exists int
	err := database.DB.QueryRow("SELECT COUNT(*) FROM manga WHERE id = ?", req.MangaID).Scan(&exists)
	if err != nil || exists == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Manga not found"})
		return
	}

	// Insert or update library entry
	_, err = database.DB.Exec(`
		INSERT OR REPLACE INTO user_progress (user_id, manga_id, status)
		VALUES (?, ?, ?)
	`, userID, req.MangaID, req.Status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add to library"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Added to library"})
}

func getLibraryHandler(c *gin.Context) {
	userID := c.GetString("user_id")

	rows, err := database.DB.Query(`
		SELECT m.id, m.title, up.current_chapter, up.status
		FROM user_progress up
		JOIN manga m ON up.manga_id = m.id
		WHERE up.user_id = ?
	`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch library"})
		return
	}
	defer rows.Close()

	var library []struct {
		MangaID        string `json:"manga_id"`
		Title          string `json:"title"`
		CurrentChapter int    `json:"current_chapter"`
		Status         string `json:"status"`
	}
	for rows.Next() {
		var item struct {
			MangaID        string `json:"manga_id"`
			Title          string `json:"title"`
			CurrentChapter int    `json:"current_chapter"`
			Status         string `json:"status"`
		}
		rows.Scan(&item.MangaID, &item.Title, &item.CurrentChapter, &item.Status)
		library = append(library, item)
	}
	c.JSON(http.StatusOK, gin.H{"library": library, "count": len(library)})
}

func updateProgressHandler(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		MangaID        string `json:"manga_id" binding:"required"`
		CurrentChapter int    `json:"current_chapter" binding:"gte=0"`
		Status         string `json:"status" binding:"omitempty,oneof=reading completed plan_to_read"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_, err := database.DB.Exec(`
		UPDATE user_progress 
		SET current_chapter = ?, updated_at = CURRENT_TIMESTAMP
		WHERE user_id = ? AND manga_id = ?
	`, req.CurrentChapter, userID, req.MangaID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update progress"})
		return
	}

	if req.Status != "" {
		_, err = database.DB.Exec(`
			UPDATE user_progress SET status = ? WHERE user_id = ? AND manga_id = ?
		`, req.Status, userID, req.MangaID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update status"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "Progress updated"})

	// Broadcast to TCP and UDP servers
	var mangaTitle, username string
	if err := database.DB.QueryRow("SELECT title FROM manga WHERE id = ?", req.MangaID).Scan(&mangaTitle); err != nil {
		log.Println("Error fetching manga title:", err)
		return
	}
	if err := database.DB.QueryRow("SELECT username FROM users WHERE id = ?", userID).Scan(&username); err != nil {
		log.Println("Error fetching username:", err)
		return
	}

	payload := shared.ProgressUpdate{
		UserID:         userID,
		Username:       username,
		MangaID:        req.MangaID,
		MangaTitle:     mangaTitle,
		CurrentChapter: req.CurrentChapter,
		Status:         req.Status,
		Timestamp:      time.Now().Unix(),
	}

	jsonData, _ := json.Marshal(payload)

	// Post to TCP
	go func() {
		resp, err := http.Post(tcpServerURL, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			log.Println("Failed to broadcast to TCP server:", err)
		} else {
			resp.Body.Close()
		}
	}()

	// Post to UDP (added)
	go func() {
		resp, err := http.Post(udpServerURL, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			log.Println("Failed to broadcast to UDP server:", err)
		} else {
			resp.Body.Close()
		}
	}()
}
