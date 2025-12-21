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
		AllowOrigins:     []string{"http://127.0.0.1:5500", "http://localhost:5500", "http://localhost:8080"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	router.GET("/", func(c *gin.Context) {
		c.File(filepath.Join("web", "search_manga.html"))
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
	log.Println("Make sure TCP server is running on :9091 and UDP server on :9095")
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

func getMangaDetailHandler(c *gin.Context) {
	id := c.Param("id")
	var m models.Manga
	err := database.DB.QueryRow(`SELECT id, title, author, genres, status, total_chapters, description FROM manga WHERE id = ?`, id).
		Scan(&m.ID, &m.Title, &m.Author, &m.GenresString, &m.Status, &m.TotalChapters, &m.Description)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Manga not found"})
		return
	} else if err != nil {
		log.Printf("Database error fetching manga detail: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	m.PostScan()
	c.JSON(http.StatusOK, m)
}

func getMangaHandler(c *gin.Context) {
	// Support both ?search= and ?title= (common in different HTML versions)
	query := c.Query("search")
	if query == "" {
		query = c.Query("title")
	}

	var mangaList []models.Manga
	var rows *sql.Rows
	var err error

	if query != "" {
		// Case-insensitive partial match on title
		searchTerm := "%" + query + "%"
		rows, err = database.DB.Query(`
			SELECT id, title, author, genres, status, total_chapters, description 
			FROM manga 
			WHERE LOWER(title) LIKE LOWER(?)
			ORDER BY title
		`, searchTerm)
		log.Printf("Searching manga with query: '%s' (using term: '%s')", query, searchTerm)
	} else {
		rows, err = database.DB.Query(`
			SELECT id, title, author, genres, status, total_chapters, description 
			FROM manga 
			ORDER BY title
		`)
		log.Println("Fetching all manga (no search query)")
	}

	if err != nil {
		log.Printf("Database error fetching manga: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch manga"})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var m models.Manga
		err := rows.Scan(&m.ID, &m.Title, &m.Author, &m.GenresString, &m.Status, &m.TotalChapters, &m.Description)
		if err != nil {
			log.Printf("Scan error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Scan error"})
			return
		}
		m.PostScan()
		mangaList = append(mangaList, m)
	}

	log.Printf("Returning %d manga result(s) for query '%s'", len(mangaList), query)
	c.JSON(http.StatusOK, gin.H{
		"manga": mangaList,
		"count": len(mangaList),
	})
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
		Status  string `json:"status" binding:"oneof=reading completed plan_to_read """`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check manga exists
	var exists int
	err := database.DB.QueryRow("SELECT COUNT(*) FROM manga WHERE id = ?", req.MangaID).Scan(&exists)
	if err != nil || exists == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Manga not found"})
		return
	}

	var status string
	if req.Status != "" {
		status = req.Status
	} else {
		status = "reading"
	}

	_, err = database.DB.Exec(`
		INSERT INTO user_progress (user_id, manga_id, current_chapter, status)
		VALUES (?, ?, 0, ?)
		ON CONFLICT(user_id, manga_id) DO UPDATE SET status = excluded.status
	`, userID, req.MangaID, status)
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
	username := c.GetString("username")

	var req struct {
		MangaID        string `json:"manga_id" binding:"required"`
		CurrentChapter int    `json:"current_chapter" binding:"gte=0"`
		Status         string `json:"status" binding:"omitempty,oneof=reading completed plan_to_read"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("Bad progress update request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("PROGRESS UPDATE REQUEST RECEIVED")
	log.Printf("   UserID: %s (%s)", userID, username)
	log.Printf("   MangaID: %s", req.MangaID)
	log.Printf("   New Chapter: %d", req.CurrentChapter)
	if req.Status != "" {
		log.Printf("   New Status: %s", req.Status)
	}

	_, err := database.DB.Exec(`
		UPDATE user_progress 
		SET current_chapter = ?, updated_at = CURRENT_TIMESTAMP
		WHERE user_id = ? AND manga_id = ?
	`, req.CurrentChapter, userID, req.MangaID)
	if err != nil {
		log.Printf("Database error updating chapter: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update progress"})
		return
	}

	if req.Status != "" {
		_, err = database.DB.Exec(`
			UPDATE user_progress SET status = ? WHERE user_id = ? AND manga_id = ?
		`, req.Status, userID, req.MangaID)
		if err != nil {
			log.Printf("Database error updating status: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update status"})
			return
		}
	}

	log.Println("Database updated successfully")

	var mangaTitle string
	if err := database.DB.QueryRow("SELECT title FROM manga WHERE id = ?", req.MangaID).Scan(&mangaTitle); err != nil {
		log.Printf("Error fetching manga title: %v", err)
		mangaTitle = "Unknown Manga"
	}

	if username == "" {
		if err := database.DB.QueryRow("SELECT username FROM users WHERE id = ?", userID).Scan(&username); err != nil {
			log.Printf("Error fetching username: %v", err)
			username = "Unknown User"
		}
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

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error marshaling payload: %v", err)
		c.JSON(http.StatusOK, gin.H{"message": "Progress updated (broadcast failed)"})
		return
	}

	log.Printf("Broadcasting progress update to TCP and UDP servers...")
	log.Printf("   Payload: %s", string(jsonData))

	go func() {
		resp, err := http.Post(tcpServerURL, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			log.Printf("FAILED to broadcast to TCP server (%s): %v", tcpServerURL, err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.Printf("TCP server responded with status: %d", resp.StatusCode)
		} else {
			log.Printf("Successfully broadcasted to TCP server")
		}
	}()

	go func() {
		resp, err := http.Post(udpServerURL, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			log.Printf("FAILED to broadcast to UDP server (%s): %v", udpServerURL, err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.Printf("UDP server responded with status: %d", resp.StatusCode)
		} else {
			log.Printf("Successfully broadcasted to UDP server")
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "Progress updated and broadcasted"})
}
