package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/NuZard84/go-socket-speedscript/internal/db"
	"github.com/NuZard84/go-socket-speedscript/internal/game"
	"github.com/NuZard84/go-socket-speedscript/internal/handlers"
	"github.com/joho/godotenv"
)

// Initialize logging configuration
func init() {
	godotenv.Load()

	if err := db.Connect(os.Getenv("MONGO_URI")); err != nil {
		log.Fatal("Could not connect to MongoDB:", err)
	}

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
}

// middleware function at the top level
func enableCORS(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:3000")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		handler(w, r)
	}
}

// Main server function
func main() {
	handlers.Init()
	game.SetRoomManager(handlers.RoomManager)

	http.HandleFunc("/ws/room", handlers.HandleWebSocket)

	http.HandleFunc("/api/create-room", enableCORS(handlers.HandleCreateRoom))
	http.HandleFunc("/api/check-room", enableCORS(handlers.HandleCheckRoom))

	port := ":8080"
	log.Printf("Server starting on http://localhost%s", port)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		log.Println("Shutting down server...")
		os.Exit(0)
	}()

	log.Fatal(http.ListenAndServe(port, nil))
}
