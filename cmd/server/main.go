package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/NuZard84/go-socket-speedscript/internal/db"
	"github.com/NuZard84/go-socket-speedscript/internal/game"
	"github.com/NuZard84/go-socket-speedscript/internal/handlers"
	"github.com/joho/godotenv"
)

// init loads environment variables and connects to MongoDB.
func init() {
	_ = godotenv.Load() // Load .env file if running locally

	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		log.Fatal("MONGO_URI not set in environment variables")
	}

	if err := db.Connect(mongoURI); err != nil {
		log.Fatalf("Could not connect to MongoDB: %v", err)
	}

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	log.Println("MongoDB connection established successfully")
}

func main() {
	// Initialize handlers and the global room manager.
	handlers.Init()
	game.SetRoomManager(handlers.RoomManager)

	// Set up the HTTP routes
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/room", handlers.HandleWebSocket)
	mux.HandleFunc("/api/create-room", handlers.EnableCORS(handlers.HandleCreateRoom))
	mux.HandleFunc("/api/check-room", handlers.EnableCORS(handlers.HandleCheckRoom))
	mux.HandleFunc("/api/test", handlers.EnableCORS(handlers.HandleTestAPI))

	// Apply security headers middleware
	handler := handlers.SecurityHeadersMiddleware(mux)

	// Read port from environment
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := "0.0.0.0:" + port

	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start the server
	go func() {
		log.Printf("🚀 Server starting on port %s...", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Graceful shutdown handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("🛑 Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}

	log.Println("✅ Server gracefully stopped")
}
