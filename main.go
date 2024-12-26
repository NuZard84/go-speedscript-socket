package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/NuZard84/go-socket-speedscript/db"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

// Game state and configuration constants
const (
	StatusWaiting    = "waiting"
	StatusCountdown  = "countdown"
	StatusInProgress = "in_progress"
	StatusFinished   = "finished"
	StatusReseting   = "game_reset"

	MinPlayersToStart = 2
	CountdownDuration = 3 * time.Second
)

// Client represents a connected player with their connection and game stats
type Client struct {
	Conn     *websocket.Conn
	Username string
	Room     *Room
	Stats    *PlayerStats
	mu       sync.RWMutex
}

// PlayerStats tracks individual player performance during the game
type PlayerStats struct {
	IsReady    bool       `json:"isReady"`
	Progress   float64    `json:"progress"`
	WPM        float64    `json:"wpm"`
	FinishTime *time.Time `json:"finishTime,omitempty"`
	Rank       int        `json:"rank"`
}

// Room represents a game room where multiple players compete
type Room struct {
	ID        string
	Clients   map[string]*Client
	Text      string
	Status    string
	StartTime *time.Time
	NextRank  int
	mutex     sync.RWMutex
}

// Message defines the structure for WebSocket communication
type Message struct {
	Type     string      `json:"type"`
	Username string      `json:"username"`
	RoomID   string      `json:"room_id"`
	Data     interface{} `json:"data"`
	Time     time.Time   `json:"timestamp"`
	Text     string      `json:"text"`
}

// RoomManager handles the creation and management of game rooms
type RoomManager struct {
	Rooms       map[string]*Room
	mutex       sync.RWMutex
	maxRooms    int
	activeRooms int
}

// RoomConfiguration holds room-specific settings : for now hold for this feature
// type RoomConfiguration struct {
// 	MaxPlayers       int
// 	MinPlayers       int
// 	CountdownSeconds int
// 	TextDifficulty   string
// 	TimeLimit        time.Duration
// }

// Global variables for WebSocket and room management
var (
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true // Configure appropriately for production
		},
	}
	roomManager = NewRoomManager(10)
)

func setTextFromDb() string {

	ctx := context.Background()
	sentence, err := db.GetRandomSentence(ctx)

	if err != nil {
		log.Printf("Error fetching random sentence: %v", err)
		return "This is a sample text"
	}
	return sentence.Story
}

// NewRoomManager creates a new room manager instance
func NewRoomManager(maxRooms int) *RoomManager {
	log.Printf("Creating new room manager with max rooms: %d", maxRooms)
	return &RoomManager{
		Rooms:    make(map[string]*Room),
		maxRooms: maxRooms,
	}
}

// NewClient creates a new client instance with initialized stats
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

// NewRoom creates a new game room with the given ID
func NewRoom(id string) *Room {
	log.Printf("Creating new room: %s", id)

	log.Print("Generating random Text...")

	text := setTextFromDb()

	return &Room{
		ID:      id,
		Clients: make(map[string]*Client),
		Status:  StatusWaiting,
		Text:    text,
	}

}

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
	http.HandleFunc("/ws/room", handleWebSocket)

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

// handleWebSocket manages new WebSocket connections
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	roomID := r.URL.Query().Get("room_id")

	if username == "" || roomID == "" {
		http.Error(w, "Missing username or room_id", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	client := NewClient(conn, username)
	room := getOrCreateRoom(roomID)

	if err := room.AddClient(client); err != nil {
		log.Printf("Failed to add user to room: %v", err)
		conn.WriteJSON(Message{
			Type: "error",
			Data: err.Error(),
		})
		conn.Close()
		return
	}

	go handleClientMessage(room, client)
}

// handleClientMessage processes incoming messages from clients
func handleClientMessage(room *Room, client *Client) {
	defer room.RemoveClient(client)

	for {
		var msg Message
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
		}
	}
}

// handleReadyState processes player ready status updates
func handleReadyState(room *Room, client *Client, msg Message) {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	readyState, ok := msg.Data.(bool)
	if !ok {
		log.Printf("Invalid data type for ready state: %v", msg.Data)
		return
	}

	client.mu.Lock()
	client.Stats.IsReady = readyState
	client.mu.Unlock()

	allReady := true
	clientCount := 0
	for _, c := range room.Clients {
		c.mu.RLock()
		if !c.Stats.IsReady {
			allReady = false
		}
		clientCount++
		c.mu.RUnlock()
	}

	if allReady && clientCount >= MinPlayersToStart && room.Status == StatusWaiting {
		go room.startGame()
	}

	go room.broadcastRoomState()
}

// handleProgress updates player progress during the game
func handleProgress(room *Room, client *Client, msg Message) {
	room.mutex.RLock()
	if room.Status != StatusInProgress {
		room.mutex.RUnlock()
		return
	}
	room.mutex.RUnlock()

	client.mu.Lock()
	if client.Stats.FinishTime != nil {
		client.mu.Unlock()
		return
	}

	if progress, ok := msg.Data.(map[string]interface{}); ok {
		if p, ok := progress["progress"].(float64); ok {
			client.Stats.Progress = p
		}
		if w, ok := progress["wpm"].(float64); ok {
			client.Stats.WPM = w
		}
	}

	isFinished := client.Stats.Progress >= 100
	client.mu.Unlock()

	if isFinished {
		room.handleClientFinish(client)
	} else {
		room.broadcastRoomState()
	}
}

// handlePing responds to client ping messages
func handlePing(client *Client) {
	client.mu.Lock()
	defer client.mu.Unlock()

	client.Conn.WriteJSON(Message{
		Type: "pong",
		Data: time.Now(),
	})
}

// startGame initiates the game countdown and start sequence
func (room *Room) startGame() {
	room.mutex.Lock()
	room.Status = StatusCountdown
	room.mutex.Unlock()

	for i := 3; i > 0; i-- {
		room.BroadcastMessage(Message{
			Type: "countdown",
			Data: i,
		})
		time.Sleep(time.Second)
	}

	room.mutex.Lock()
	now := time.Now()
	room.StartTime = &now
	room.Status = StatusInProgress
	room.NextRank = 1
	room.mutex.Unlock()

	room.broadcastRoomState()
}

// handleClientFinish processes a player finishing the game
func (room *Room) handleClientFinish(client *Client) {
	room.mutex.Lock()

	if room.Status != StatusInProgress {
		room.mutex.Unlock()
		return
	}

	client.mu.RLock()
	if client.Stats.FinishTime != nil {
		client.mu.RUnlock()
		room.mutex.Unlock()
		return
	}
	client.mu.RUnlock()

	now := time.Now()

	client.mu.Lock()
	client.Stats.FinishTime = &now
	client.Stats.Progress = 100.0
	client.Stats.Rank = room.NextRank
	wpm := client.Stats.WPM
	log.Printf("user: %s at finish_state in room: %s", client.Username, room.ID)
	client.mu.Unlock()

	room.NextRank++

	finishEvent := Message{
		Type:   "user_finished",
		RoomID: room.ID,
		Data: map[string]interface{}{
			"rank":     client.Stats.Rank,
			"wpm":      wpm,
			"time":     now.Sub(*room.StartTime).Seconds(),
			"username": client.Username,
		},
	}

	allFinished := true
	for _, c := range room.Clients {
		c.mu.RLock()
		if c.Stats.FinishTime == nil {
			allFinished = false
		}
		c.mu.RUnlock()
	}

	if allFinished {
		log.Printf("All players have finished in room %s", room.ID)
		room.Status = StatusFinished
	}

	room.mutex.Unlock()

	room.BroadcastMessage(finishEvent)
	room.broadcastRoomState()

	if allFinished {
		room.handleGameFinished()
	}
}

// handleGameFinished processes the end of a game
func (room *Room) handleGameFinished() {
	log.Printf("Game has finished in room %s", room.ID)
	room.BroadcastMessage(Message{
		Type: "game_finished",
		Data: map[string]interface{}{
			"message": "Game has finished",
			"status":  StatusFinished,
		},
	})

	room.broadcastRoomState()
}

// broadcastRoomState sends current room state to all clients
func (room *Room) broadcastRoomState() {
	room.mutex.RLock()
	state := struct {
		Status    string                  `json:"status"`
		Players   map[string]*PlayerStats `json:"players"`
		Text      string                  `json:"text,omitempty"`
		StartTime *time.Time              `json:"startTime,omitempty"`
	}{
		Status:    room.Status,
		Players:   make(map[string]*PlayerStats),
		Text:      room.Text,
		StartTime: room.StartTime,
	}

	for username, client := range room.Clients {
		client.mu.RLock()
		stats := &PlayerStats{
			IsReady:  client.Stats.IsReady,
			Progress: math.Round(client.Stats.Progress*100) / 100,
			WPM:      math.Round(client.Stats.WPM*100) / 100,
			Rank:     client.Stats.Rank,
		}
		if client.Stats.FinishTime != nil {
			finishTime := *client.Stats.FinishTime
			stats.FinishTime = &finishTime
		}
		state.Players[username] = stats
		client.mu.RUnlock()
	}
	room.mutex.RUnlock()

	room.BroadcastMessage(Message{
		Type:   "room_state",
		Data:   state,
		Time:   time.Now(),
		RoomID: room.ID,
		Text:   room.Text,
	})
}

// AddClient adds a new client to the room
func (room *Room) AddClient(client *Client) error {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	if room.Status != StatusWaiting {
		return fmt.Errorf("this room is already in busy state")
	}

	if _, ok := room.Clients[client.Username]; ok {
		return fmt.Errorf("this username is already taken")
	}

	room.Clients[client.Username] = client
	client.Room = room

	go room.broadcastRoomState()
	return nil
}

// RemoveClient removes a client from the room
func (room *Room) RemoveClient(client *Client) {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	if client == nil {
		return
	}

	client.mu.Lock()
	if client.Conn != nil {
		client.Conn.Close()
	}
	client.mu.Unlock()

	delete(room.Clients, client.Username)

	if len(room.Clients) < MinPlayersToStart && room.Status != StatusWaiting {
		room.Status = StatusWaiting
		room.StartTime = nil

		for _, c := range room.Clients {
			c.mu.Lock()
			c.Stats.IsReady = false
			c.Stats.Progress = 0
			c.Stats.WPM = 0
			c.mu.Unlock()
		}

		go room.BroadcastMessage(Message{
			Type: StatusReseting,
			Data: "Game reset: Not enough players",
		})
	}

	if len(room.Clients) == 0 {
		go roomManager.RemoveRoom(room.ID)
	}
}

// BroadcastMessage sends a message to all clients in the room
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

	go func() {
		wg.Wait()
		close(errorsChan)
	}()

	var errList []error
	for err := range errorsChan {
		errList = append(errList, err)
	}

	if len(errList) > 0 {
		log.Printf("Errors broadcasting message: %v", errList)
	}
}

// RemoveRoom removes a room from the room manager
func (rm *RoomManager) RemoveRoom(roomID string) {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	if _, ok := rm.Rooms[roomID]; !ok {
		log.Printf("Room does not exist!")
		return
	}

	delete(rm.Rooms, roomID)
	rm.activeRooms--
	log.Printf("Room removed: %s, Active rooms: %d", roomID, rm.activeRooms)
}

// getOrCreateRoom retrieves an existing room or creates a new one
func getOrCreateRoom(roomID string) *Room {
	roomManager.mutex.Lock()
	defer roomManager.mutex.Unlock()

	if room, ok := roomManager.Rooms[roomID]; ok {
		log.Printf("Room already exists: %s", roomID)
		return room
	}

	room := NewRoom(roomID)
	roomManager.Rooms[roomID] = room
	roomManager.activeRooms++
	log.Printf("Created new room: %s", roomID)
	return room
}
