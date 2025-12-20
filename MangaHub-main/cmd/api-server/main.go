package main

import (
	"database/sql"
	"log"
	"mangahub/internal/auth"
	"mangahub/internal/database"
	"mangahub/internal/tcp"
	"mangahub/internal/udp"
	"mangahub/pkg/models"
	"net/http"
	"path/filepath"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const tcpServerURL = "http://localhost:9091/internal/progress"

func main() {
	if err := database.Initialize("./data/mangahub.db"); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	defer database.Close()

	progressHub = tcp.GlobalHub
	go progressHub.Run() // Start broadcasting goroutine

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

	// Serve the web interface
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
		protected.GET("/manga", getMangaHandler) // Now protected
		protected.GET("/manga/:id", getMangaDetailHandler)
		protected.POST("/manga", createMangaHandler)
		protected.POST("/users/library", addToLibraryHandler)
		protected.GET("/users/library", getLibraryHandler)
		protected.PUT("/users/progress", updateProgressHandler)
		// Add more endpoints here soon
	}

	go udp.GlobalHub.Run()
	udp.StartUDPListener(":9091")
	log.Println("UDP Notification Server started on :9091")

	// Start http server
	log.Println("Server starting on http://localhost:8080")
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
		c.JSON(http.StatusConflict, gin.H{"error": "Username or email already exists"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message":  "User registered successfully",
		"user_id":  userID,
		"username": req.Username,
	})
}

func loginHandler(c *gin.Context) {
	var req models.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user models.User
	err := database.DB.QueryRow(`SELECT id, username, password_hash FROM users WHERE username = ?`, req.Username).
		Scan(&user.ID, &user.Username, &user.PasswordHash)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
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

	c.JSON(http.StatusOK, models.LoginResponse{
		Token:    token,
		Username: user.Username,
		UserID:   user.ID,
	})
}

// getMangaHandler handles manga search
func getMangaHandler(c *gin.Context) {
	title := c.Query("title")
	genre := c.Query("genre")
	status := c.Query("status")

	query := `SELECT id, title, author, genres, status, total_chapters, description FROM manga WHERE 1=1`
	args := []interface{}{}

	if title != "" {
		query += ` AND title LIKE ?`
		args = append(args, "%"+title+"%")
	}
	if genre != "" {
		query += ` AND genres LIKE ?`
		args = append(args, "%"+genre+"%")
	}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}

	rows, err := database.DB.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database query failed"})
		return
	}
	defer rows.Close()

	var mangaList []models.Manga
	for rows.Next() {
		var manga models.Manga
		if err := rows.Scan(&manga.ID, &manga.Title, &manga.Author, &manga.GenresString, &manga.Status, &manga.TotalChapters, &manga.Description); err != nil {
			continue
		}
		manga.PostScan()
		mangaList = append(mangaList, manga)
	}

	c.JSON(http.StatusOK, gin.H{"results": mangaList, "count": len(mangaList)})
}

func getMangaDetailHandler(c *gin.Context) {
	id := c.Param("id")
	var manga models.Manga
	err := database.DB.QueryRow(
		"SELECT id, title, author, genres, status, total_chapters, description FROM manga WHERE id = ?",
		id,
	).Scan(&manga.ID, &manga.Title, &manga.Author, &manga.GenresString, &manga.Status, &manga.TotalChapters, &manga.Description)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Manga not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	manga.PostScan()
	c.JSON(http.StatusOK, manga)
}

func createMangaHandler(c *gin.Context) {
	var manga models.Manga
	if err := c.ShouldBindJSON(&manga); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	manga.ID = uuid.New().String() // Generate ID
	manga.PreSave()                // Convert genres array to string

	_, err := database.DB.Exec(
		"INSERT INTO manga (id, title, author, genres, status, total_chapters, description) VALUES (?, ?, ?, ?, ?, ?, ?)",
		manga.ID, manga.Title, manga.Author, manga.GenresString, manga.Status, manga.TotalChapters, manga.Description,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create manga"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "Manga created", "manga": manga})
}

func addToLibraryHandler(c *gin.Context) {
	userID := c.GetString("user_id")
	var req struct {
		MangaID string `json:"manga_id" binding:"required"`
		Status  string `json:"status" binding:"required,oneof=reading completed plan_to_read"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_, err := database.DB.Exec(
		"INSERT OR REPLACE INTO user_progress (user_id, manga_id, status) VALUES (?, ?, ?)",
		userID, req.MangaID, req.Status,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add to library"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Added to library"})
}

func getLibraryHandler(c *gin.Context) {
	userID := c.GetString("user_id")
	rows, err := database.DB.Query(
		"SELECT m.id, m.title, p.current_chapter, p.status FROM user_progress p JOIN manga m ON p.manga_id = m.id WHERE p.user_id = ?",
		userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
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

	// Fetch manga title and username safely
	var mangaTitle, username string
	if err := database.DB.QueryRow("SELECT title FROM manga WHERE id = ?", req.MangaID).Scan(&mangaTitle); err != nil {
		mangaTitle = "Unknown Manga"
	}
	if err := database.DB.QueryRow("SELECT username FROM users WHERE id = ?", userID).Scan(&username); err != nil {
		username = "Unknown User"
	}

	// Broadcast to TCP and UDP
	progress := models.UserProgress{
		UserID:         userID,
		MangaID:        req.MangaID,
		CurrentChapter: req.CurrentChapter,
		Status:         req.Status,
	}

	progressHub.BroadcastProgress(progress, username, mangaTitle)
	udp.GlobalHub.BroadcastProgress(progress, username, mangaTitle)

	// Update database
	_, err := database.DB.Exec(
		"UPDATE user_progress SET current_chapter = ?, status = ?, updated_at = CURRENT_TIMESTAMP WHERE user_id = ? AND manga_id = ?",
		req.CurrentChapter, req.Status, userID, req.MangaID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update progress"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Progress updated"})
}
