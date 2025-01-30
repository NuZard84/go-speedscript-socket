package handlers

import (
	"log"
	"net/http"
	"time"

	"github.com/NuZard84/go-socket-speedscript/internal/constants"
	"github.com/NuZard84/go-socket-speedscript/internal/game"
	"github.com/NuZard84/go-socket-speedscript/internal/manager"
	"github.com/NuZard84/go-socket-speedscript/internal/models"
	"github.com/gorilla/websocket"
)

// VARIABLES =>

// Configure WebSocket upgrader
var Upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// TODO: In production, implement proper origin checking
		return true
	},
}

// Global room manager instance
var RoomManager *manager.RoomManager

func Init() {
	RoomManager = manager.NewRoomManager(10)
}

// METHODS =>

// handleWebSocket manages new WebSocket connections
func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")

	if username == "" {
		http.Error(w, "Missing username", http.StatusBadRequest)
		return
	}

	conn, err := Upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	client := game.NewClient(conn, username)
	room := RoomManager.FindOrCreateRoom()

	if err := room.AddClient(client); err != nil {
		log.Printf("Failed to add user to room: %v", err)
		conn.WriteJSON(models.Message{
			Type: "error",
			Data: err.Error(),
		})
		conn.Close()
		return
	}

	go HandleClientMessage(room, client)
}

// handleClientMessage processes incoming messages from clients
func HandleClientMessage(room *game.Room, client *game.Client) {
	defer room.RemoveClient(client)

	for {
		var msg models.Message
		err := client.Conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error for user %s: %v", client.Username, err)
			}
			return
		}

		msg.Username = client.Username
		msg.Time = time.Now()

		switch msg.Type {
		case "ready":
			handleReadyState(room, client, msg)
		case "progress":
			handleProgress(room, client, msg)
		case "ping":
			handlePing(client)
		case "final_stats":
			log.Printf("Received final_stats message: %+v", msg)
			room.HandleFinalStats(client, msg)
		case "timeout":
			room.HandleTimeout()
		}
	}
}

// handleReadyState processes player ready status updates
func handleReadyState(room *game.Room, client *game.Client, msg models.Message) {
	readyState, ok := msg.Data.(bool)
	if !ok {
		log.Printf("Invalid ready state: %v", msg.Data)
		return
	}

	client.Mu.Lock()
	client.Stats.IsReady = readyState
	client.Mu.Unlock()

	if room.ValidateAllPlayersReady() {
		go room.StartGame()
	}

	go room.BroadcastRoomState()
}

// handleProgress updates player progress during the game
func handleProgress(room *game.Room, client *game.Client, msg models.Message) {
	room.Mutex.RLock()
	if room.Status != constants.StatusInProgress {
		room.Mutex.RUnlock()
		return
	}
	room.Mutex.RUnlock()

	client.Mu.Lock()
	if client.Stats.FinishTime != nil {
		client.Mu.Unlock()
		return
	}

	var totalChars int
	if progress, ok := msg.Data.(map[string]interface{}); ok {
		if pos, ok := progress["currentPosition"].(float64); ok {
			client.Stats.CurrentPosition = int(pos)
		}
		if total, ok := progress["totalCharacters"].(float64); ok {
			totalChars = int(total)
		}
		if w, ok := progress["wpm"].(float64); ok {
			client.Stats.WPM = w
		}
	}

	isFinished := client.Stats.CurrentPosition >= totalChars
	client.Mu.Unlock()

	if isFinished {
		room.HandleClientFinish(client)
	} else {
		room.BroadcastRoomState()
	}
}

// handlePing responds to client ping messages
func handlePing(client *game.Client) {
	client.Mu.Lock()
	defer client.Mu.Unlock()

	client.Conn.WriteJSON(models.Message{
		Type: "pong",
		Data: time.Now(),
	})
}
