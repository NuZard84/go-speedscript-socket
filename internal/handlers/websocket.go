package handlers

import (
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/NuZard84/go-socket-speedscript/internal/constants"
	"github.com/NuZard84/go-socket-speedscript/internal/game"
	"github.com/NuZard84/go-socket-speedscript/internal/manager"
	"github.com/NuZard84/go-socket-speedscript/internal/models"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

// Global variables
var (
	globalClients   = make(map[*game.Client]bool) // Track all global online clients
	globalClientsMu sync.RWMutex                  // Mutex to protect globalClients
)

// allowedOrigins reads a comma‑separated list of origins from the ALLOWED_ORIGINS
// environment variable. If not set, it defaults to production and local URLs.
var allowedOrigins = func() []string {
	_ = godotenv.Load()

	origins := os.Getenv("ALLOWED_ORIGINS")

	parts := strings.Split(origins, ",")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}
	return parts
}()

// Upgrader is configured for WebSocket connections.
// The CheckOrigin function allows only requests from allowed origins.
var Upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		for _, o := range allowedOrigins {
			if o == origin {
				return true
			}
		}
		log.Printf("Rejected WebSocket connection from origin: %s", origin)
		return false
	},
}

// RoomManager is the global instance for managing game rooms.
var RoomManager *manager.RoomManager

// Init initializes the RoomManager.
func Init() {
	RoomManager = manager.NewRoomManager(5000)
}

// HandleWebSocket upgrades HTTP connections to WebSockets and assigns clients to rooms.
func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	roomID := r.URL.Query().Get("room_id")

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

	var room *game.Room

	if roomID != "" {
		existingRoom, err := RoomManager.GetRoom(roomID, true)
		if err != nil {
			conn.WriteJSON(models.Message{
				Type: "error",
				Data: "Room not found",
			})
			conn.Close()
			return
		}
		room = existingRoom
	} else {
		room = RoomManager.FindOrCreateRoom()
	}

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

//dummy client

func NewDummyClient(conn *websocket.Conn, username string) *game.Client {
	return &game.Client{
		Conn:     conn,
		Username: username,
	}
}

// HandleGlobalOnlineWebSocket upgrades HTTP connections to WebSockets for global online count.

func HandleGlobalOnlineWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := Upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	// Create a new client without a username or room
	client := NewDummyClient(conn, uuid.NewString())

	// Add the client to the globalClients map
	globalClientsMu.Lock()
	globalClients[client] = true
	globalClientsMu.Unlock()

	// Start a goroutine to handle sending the global online count
	go HandleGlobalOnlineClient(client)
}

// HandleGlobalOnlineClient sends the global online count to the client periodically.

func HandleGlobalOnlineClient(client *game.Client) {
	defer func() {
		// Remove the client from the globalClients map when they disconnect
		globalClientsMu.Lock()
		delete(globalClients, client)
		globalClientsMu.Unlock()

		client.Conn.Close()
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Calculate the global online count of all connected clients
		globalClientsMu.RLock()
		onlineCount := len(globalClients)
		globalClientsMu.RUnlock()

		// Create the message payload
		message := models.Message{
			Type: "global_online",
			Data: onlineCount, // an integer representing the total users connected
			Time: time.Now(),
		}

		// Send the message to the client
		client.WriteMu.Lock()
		err := client.Conn.WriteJSON(message)
		client.WriteMu.Unlock()

		if err != nil {
			log.Printf("Error sending global online count to client: %v", err)
			return
		}
	}
}

func handleResetState(room *game.Room, client *game.Client) {
	// OPTIONAL: If you want only the admin to reset, do something like:
	// if !room.IsAdmin(client.Username) {
	//     client.Conn.WriteJSON(models.Message{
	//         Type: "error",
	//         Data: "Only admin can reset the room",
	//     })
	//     return
	// }

	if err := room.HandleResetRoomState(); err != nil {
		log.Printf("Error resetting room state: %v", err)

		client.Conn.WriteJSON(models.Message{
			Type: "error",
			Data: err.Error(),
		})
		return
	}

	client.Conn.WriteJSON(models.Message{
		Type: "reseted_room",
	})
	room.BroadcastRoomState()
}

// HandleClientMessage reads messages from the WebSocket and handles them accordingly.
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
		case "admin_action":
			var adminAction game.AdminAction
			if data, ok := msg.Data.(map[string]interface{}); ok {
				action := data["action"].(string)
				maxCap, _ := data["maxCapacity"].(float64)
				if action == constants.AdminActionKick {
					adminAction = game.AdminAction{
						Action: data["action"].(string),
						Target: data["target"].(string),
						RoomID: data["room_id"].(string),
					}
				}
				if action == constants.AdminActionUpdateCapacity {
					adminAction = game.AdminAction{
						Action:      data["action"].(string),
						MaxCapacity: int(maxCap),
						RoomID:      data["room_id"].(string),
					}
				}
				if err := room.HandleAdminAction(adminAction, client); err != nil {
					client.WriteMu.Lock()
					client.Conn.WriteJSON(models.Message{
						Type: "error",
						Data: err.Error(),
					})
					client.WriteMu.Unlock()
				}
			}
		case "ready":
			handleReadyState(room, client, msg)
		case "progress":
			handleProgress(room, client, msg)
		case "wpm_update":
			if data, ok := msg.Data.(map[string]interface{}); ok {
				if wpmValue, ok := data["wpm"].(float64); ok {
					if client.Room != nil {
						client.Room.HandleClientWpmUpdate(client, wpmValue)
					}
				}
			}
		case "ping":
			handlePing(client)
		case "reset_state":
			handleResetState(room, client)
		case "final_stats":
			log.Printf("Received final_stats message: %+v", msg)
			room.HandleFinalStats(client, msg)
		case "timeout":
			room.HandleTimeout()
		}
	}
}

// handleReadyState processes the player's ready status.
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

// handleProgress updates the player's progress during the game.
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

// handlePing replies to ping messages from the client.
func handlePing(client *game.Client) {
	client.Mu.Lock()
	defer client.Mu.Unlock()
	client.Conn.WriteJSON(models.Message{
		Type: "pong",
		Data: time.Now(),
	})
}
