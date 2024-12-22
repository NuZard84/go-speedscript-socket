package main

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Constants for game states and configuration
const (
	StatusWaiting    = "waiting"
	StatusCountdown  = "countdown"
	StatusInProgress = "in_progress"
	StatusFinished   = "finished"
	StatusReseting   = "game_reset"

	MinPlayersToStart = 2
	CountdownDuration = 3 * time.Second
)

// Client represents an individual player in the typing game
type Client struct {
	Conn     *websocket.Conn
	Username string
	Room     *Room        // Reference to the room the client is in
	Stats    *PlayerStats // Separate struct for player statistics
	mu       sync.RWMutex
}

// PlayerStats tracks player's game performance
type PlayerStats struct {
	IsReady    bool
	Progress   float64 // Temparory used :  0 to 100
	WPM        float64
	FinishTime time.Time
	// Errors     int
	// Accuracy   float64
}

// Room represents a game room where multiple players compete
type Room struct {
	ID        string
	Clients   map[string]*Client
	Text      string
	Status    string
	StartTime time.Time
	mutex     sync.RWMutex
	// Configuration RoomConfiguration
}

// RoomConfiguration holds room-specific settings : for now hold for this feature
// type RoomConfiguration struct {
// 	MaxPlayers       int
// 	MinPlayers       int
// 	CountdownSeconds int
// 	TextDifficulty   string
// 	TimeLimit        time.Duration
// }

// Message represents the WebSocket message structure
type Message struct {
	Type     string      `json:"type"`
	Username string      `json:"username"`
	RoomID   string      `json:"room_id"`
	Data     interface{} `json:"data"`
	Time     time.Time   `json:"timestamp"`
}

// RoomManager handles the creation and management of game rooms
type RoomManager struct {
	Rooms       map[string]*Room
	mutex       sync.RWMutex
	maxRooms    int
	activeRooms int
}

// Create a new RoomManager instance
func NewRoomManager(maxRooms int) *RoomManager {
	log.Printf("Creating new room manager with max rooms: %d", maxRooms)
	return &RoomManager{
		Rooms:    make(map[string]*Room),
		maxRooms: maxRooms,
	}
}

// Create a new Client instance
func NewClient(conn *websocket.Conn, username string) *Client {
	log.Printf("New client connected: %s", username)
	return &Client{
		Conn:     conn,
		Username: username,
		Stats: &PlayerStats{
			Progress: 0,
			WPM:      0,
		},
	}
}

// Create a new Room instance
func NewRoom(id string) *Room {
	log.Printf("Creating new room: %s", id)
	return &Room{
		ID:      id,
		Clients: make(map[string]*Client),
		Status:  StatusWaiting,
		// Configuration: config,
	}
}

func init() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
}

func main() {
	http.HandleFunc("/ws/room/", handleWebSocket)

	port := ":8080"
	log.Printf("Server starting on %s ...", port)
	log.Printf("http://localhost%s", port)
	log.Fatal(http.ListenAndServe(port, nil))
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Configure appropriately for production
	},
}

var roomManager = NewRoomManager(100)

func handleWebSocket(w http.ResponseWriter, r *http.Request) {

	var username = r.URL.Query().Get("username")
	var roomID = r.URL.Query().Get("room_id")

	if username == "" || roomID == "" {
		http.Error(w, "Missing username or room_id", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	//initialize client connection
	client := NewClient(conn, username)

	//get or creat a room for user
	room := getOrCreateRoom(roomID)

	//add client to room
	if err := room.AddClient(client); err != nil {
		log.Printf("Failed to add user to room: %v", err)
		conn.WriteJSON(Message{
			Type: "error",
			Data: err.Error(),
		})
		conn.Close()
		return
	}

}

func getOrCreateRoom(roomID string) *Room {
	roomManager.mutex.Lock()
	defer roomManager.mutex.Unlock()

	if room, ok := roomManager.Rooms[roomID]; ok {
		log.Printf("Room already exists: %s", roomID)
		return room
	}

	room := NewRoom(roomID)
	roomManager.Rooms[roomID] = room
	log.Printf("Created new room: %s", roomID)
	return room
}

func (room *Room) AddClient(client *Client) error {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	log.Printf("Adding username: %s to room: %s", client.Username, room.ID)

	if room.Status != StatusWaiting {
		return fmt.Errorf("this room is already in busy state")
	}

	if _, ok := room.Clients[client.Username]; ok {
		return fmt.Errorf("this username is already taken")
	}

	room.Clients[client.Username] = client
	client.Room = room

	log.Printf("Added username: %s to room: %s. Total players: %d",
		client.Username, room.ID, len(room.Clients))

	go room.broadcastRoomState()
	return nil
}

func (rm *RoomManager) RemoveRoom(roomID string) {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	if _, ok := rm.Rooms[roomID]; !ok {
		log.Printf("Room not exist !!")
		return
	}

	delete(rm.Rooms, roomID)
	rm.activeRooms--
	log.Printf("Room removed: %s, Active rooms: %d", roomID, rm.activeRooms)

}

func (room *Room) RemoveClient(client *Client) {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	if client == nil {
		log.Print("Client is nill , not exist now !!")
		return
	}

	client.mu.Lock()
	if client.Conn != nil {
		client.Conn.Close()
	}
	client.mu.Unlock()

	delete(room.Clients, client.Username)

	// Add game state management here
	if len(room.Clients) < MinPlayersToStart && room.Status != StatusWaiting {
		room.Status = StatusWaiting
		room.StartTime = time.Time{} // Reset start time

		// Reset all remaining client stats
		for _, c := range room.Clients {
			c.mu.Lock()
			c.Stats.IsReady = false
			c.Stats.Progress = 0
			c.Stats.WPM = 0
			c.mu.Unlock()
		}

		// Notify remaining clients about game reset
		go room.BroadcastMessage(Message{
			Type: StatusReseting,
			Data: "Game reset: Not enough players",
		})
	}

	if len(room.Clients) == 0 {
		go roomManager.RemoveRoom(room.ID)
	}

}

func (room *Room) broadcastRoomState() {
	room.mutex.RLock()
	state := struct {
		Status    string                  `json:"status"`
		Players   map[string]*PlayerStats `json:"players"`
		Text      string                  `json:"text,omitempty"`
		StartTime time.Time               `json:"startTime,omitempty"`
	}{
		Status:    room.Status,
		Players:   make(map[string]*PlayerStats),
		Text:      room.Text,
		StartTime: room.StartTime,
	}
	room.mutex.RUnlock()

	for username, client := range room.Clients {
		client.mu.RLock()
		state.Players[username] = client.Stats
		client.mu.RUnlock()
	}

	room.BroadcastMessage(Message{
		Type: "room_state",
		Data: state,
		Time: time.Now(),
	})
}

func (room *Room) BroadcastMessage(msg Message) {

	room.mutex.RLock()
	defer room.mutex.RUnlock()

	var wg sync.WaitGroup
	errorsChan := make(chan error, len(room.Clients))

	for _, client := range room.Clients {
		wg.Add(1)

		go func(c *Client) {
			defer wg.Done()
			c.mu.Lock()
			defer c.mu.Unlock()

			if c.Conn == nil {
				errorsChan <- fmt.Errorf("client connection is nil")
				return
			}

			if err := c.Conn.WriteJSON(msg); err != nil {
				errorsChan <- fmt.Errorf("failed to write message to client: %v", err)
				go room.RemoveClient(c)
				return
			}

		}(client)
	}

	wg.Wait()
	close(errorsChan)

	var errList []error
	for err := range errorsChan {
		errList = append(errList, err)
	}

	if len(errList) > 0 {
		log.Printf("Errors broadcasting message: %v", errList)
		return
	}

}
