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

func init() {

	_ = godotenv.Load()

	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		log.Fatal("MONGO_URI is missing in environment variables")
	}

	var err error
	for i := 0; i < 3; i++ {
		err = db.Connect(mongoURI)
		if err == nil {
			log.Println("✅ Connected to MongoDB successfully")
			break
		}
		log.Printf("Failed to connect to MongoDB (attempt %d/3): %v", i+1, err)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		log.Fatalf("Could not establish MongoDB connection after 3 attempts: %v", err)
	}

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
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
	mux.HandleFunc("/api/admin/change-role", handlers.EnableCORS(handlers.HandleAdminRoleChange))
	mux.HandleFunc("/api/test", handlers.EnableCORS(handlers.HandleTestAPI))

	// Health Check Endpoint
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	})

	// Security headers middleware
	handler := handlers.SecurityHeadersMiddleware(mux)

	// PORT from environment
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
		log.Printf("🚀 Server is running on port %s...", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server startup failed: %v", err)
		}
	}()

	// Handle Graceful Shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("🛑 Shutting down server gracefully...")

	// Context for clean shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}

	log.Println("✅ Server has stopped cleanly")
}
