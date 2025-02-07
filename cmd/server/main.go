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

// init loads the environment variables and connects to MongoDB.
func init() {
	_ = godotenv.Load()

	if err := db.Connect(os.Getenv("MONGO_URI")); err != nil {
		log.Fatal("Could not connect to MongoDB:", err)
	}

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
}

func main() {
	// Initialize handlers and the global room manager.
	handlers.Init()
	game.SetRoomManager(handlers.RoomManager)

	// Set up the HTTP mux with your routes.
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/room", handlers.HandleWebSocket)
	mux.HandleFunc("/api/create-room", handlers.EnableCORS(handlers.HandleCreateRoom))
	mux.HandleFunc("/api/check-room", handlers.EnableCORS(handlers.HandleCheckRoom))
	mux.HandleFunc("/api/test", handlers.EnableCORS(handlers.HandleTestAPI))

	// Wrap the mux with security headers middleware.
	handler := handlers.SecurityHeadersMiddleware(mux)

	// Read port from the environment (default to 8080).
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	// Create the HTTP server with sensible timeouts.
	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start the server in a goroutine.
	go func() {
		log.Printf("Server starting on http://localhost%s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Could not listen on %s: %v", addr, err)
		}
	}()

	// Wait for an interrupt signal to gracefully shutdown the server.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("Shutting down server...")

	// Create a deadline for the graceful shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server Shutdown Failed: %+v", err)
	}
	log.Println("Server gracefully stopped")
}
