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

	return nil
}
