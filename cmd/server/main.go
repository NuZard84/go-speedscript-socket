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

// Main server function
func main() {
	handlers.Init()
	game.SetRoomManager(handlers.RoomManager)
	http.HandleFunc("/ws/room", handlers.HandleWebSocket)

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
