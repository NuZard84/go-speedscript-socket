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

// Types and Structs
type Client struct {
	Conn     *websocket.Conn
	Username string
	Room     *Room
	Stats    *PlayerStats
	mu       sync.RWMutex
}

type PlayerStats struct {
	IsReady    bool
	Progress   float64
	WPM        float64
	FinishTime *time.Time
	Rank       int
}

type Room struct {
	ID        string
	Clients   map[string]*Client
	Text      string
	Status    string
	StartTime *time.Time
	NextRank  int
	mutex     sync.RWMutex
}

// RoomConfiguration holds room-specific settings : for now hold for this feature
// type RoomConfiguration struct {
// 	MaxPlayers       int
// 	MinPlayers       int
// 	CountdownSeconds int
// 	TextDifficulty   string
// 	TimeLimit        time.Duration
// }

type Message struct {
	Type     string      `json:"type"`
	Username string      `json:"username"`
	RoomID   string      `json:"room_id"`
	Data     interface{} `json:"data"`
	Time     time.Time   `json:"timestamp"`
}

type RoomManager struct {
	Rooms       map[string]*Room
	mutex       sync.RWMutex
	maxRooms    int
	activeRooms int
}

// Constructors
func NewRoomManager(maxRooms int) *RoomManager {
	log.Printf("Creating new room manager with max rooms: %d", maxRooms)
	return &RoomManager{
		Rooms:    make(map[string]*Room),
		maxRooms: maxRooms,
	}
}

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

func NewRoom(id string) *Room {
	log.Printf("Creating new room: %s", id)
	return &Room{
		ID:      id,
		Clients: make(map[string]*Client),
		Status:  StatusWaiting,
	}
}

// Main and Init
func init() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
}

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

func main() {
	http.HandleFunc("/ws/room", handleWebSocket)

	port := ":8080"
	log.Printf("Server starting on %s ...", port)
	log.Printf("http://localhost%s", port)
	log.Fatal(http.ListenAndServe(port, nil))
}

// WebSocket Handler Functions
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

// Message Handlers
func handleClientMessage(room *Room, client *Client) {
	defer room.RemoveClient(client)

	for {
		var msg Message

		// readJSON blocks until a message is received
		err := client.Conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error for user %s: %v", client.Username, err)
			}
			return
		}

		msg.Username = client.Username
		msg.Time = time.Now()

		// log.Printf("Received message from user: %s || message : %v", client.Username, msg)

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

func handleReadyState(room *Room, client *Client, msg Message) {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	//type assertion
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

func handleProgress(room *Room, client *Client, msg Message) {
	room.mutex.RLock()
	if room.Status != StatusInProgress {
		room.mutex.RUnlock()
		return
	}
	defer room.mutex.RUnlock()

	client.mu.Lock()
	defer client.mu.Unlock()

	if client.Stats.FinishTime != nil {
		return
	}

	if progress, ok := msg.Data.(map[string]interface{}); ok {
		if p, ok := progress["progress"].(float64); ok {
			client.Stats.Progress = p
			log.Printf("Progress for user %s: %f", client.Username, p)
		}
		if w, ok := progress["wpm"].(float64); ok {
			client.Stats.WPM = w
			log.Printf("WPM for user %s: %f", client.Username, w)
		}
	}

	if client.Stats.Progress >= 100 {
		log.Printf("Progress finish for user %s", client.Username)
		room.handleClientFinish(client)
	} else {
		go room.broadcastRoomState()
	}
}

func handlePing(client *Client) {
	client.mu.Lock()
	defer client.mu.Unlock()

	client.Conn.WriteJSON(Message{
		Type: "pong",
		Data: time.Now(),
	})
}

// Room Methods
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

func (room *Room) handleClientFinish(client *Client) {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	log.Printf("[DEBUG] handleClientFinish: %v", client.Username)
	now := time.Now()
	client.Stats.FinishTime = &now
	client.Stats.Progress = 100
	client.Stats.Rank = room.NextRank
	room.NextRank++

	room.BroadcastMessage(Message{
		Type:   "user_finished",
		RoomID: room.ID,
		Data: map[string]interface{}{
			"rank":     client.Stats.Rank,
			"wpm":      client.Stats.WPM,
			"time":     now.Sub(*room.StartTime).Seconds(),
			"username": client.Username,
		},
	})

	allFinished := true
	for _, c := range room.Clients {
		c.mu.RLock()
		if c.Stats.FinishTime == nil {
			allFinished = false
		}
		c.mu.RUnlock()
	}

	if allFinished {
		room.handleGameFinished()
	}
}

func (room *Room) handleGameFinished() {
	room.mutex.Lock()
	room.Status = StatusFinished
	room.mutex.Unlock()

	room.BroadcastMessage(Message{
		Type: "game_finished",
		Data: "Game has finished",
	})

	time.Sleep(5 * time.Second)
	room.resetGame()
}

func (room *Room) resetGame() {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	room.Status = StatusWaiting
	room.StartTime = nil
	room.NextRank = 1

	for _, client := range room.Clients {
		client.mu.Lock()
		client.Stats = &PlayerStats{
			Progress: 0,
			WPM:      0,
		}
		client.mu.Unlock()
	}

	room.broadcastRoomState()
}

// Room State Management
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

func (room *Room) RemoveClient(client *Client) {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	if client == nil {
		log.Print("Client is nil, not exist now!")
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

	log.Printf("room has user %v", len(room.Clients))

	if len(room.Clients) == 0 {
		go roomManager.RemoveRoom(room.ID)
	}
}

// Broadcasting
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
	room.mutex.RUnlock()

	for username, client := range room.Clients {
		client.mu.RLock()
		state.Players[username] = client.Stats
		client.mu.RUnlock()
	}

	room.BroadcastMessage(Message{
		Type:   "room_state",
		Data:   state,
		Time:   time.Now(),
		RoomID: room.ID,
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
	}
}

// Room Manager Methods
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

func getOrCreateRoom(roomID string) *Room {
	roomManager.mutex.Lock()
	defer roomManager.mutex.Unlock()

	if room, ok := roomManager.Rooms[roomID]; ok {
		log.Printf("Room already exists: %s", roomID)
		log.Printf("room : %v", room)
		return room
	}

	room := NewRoom(roomID)
	roomManager.Rooms[roomID] = room
	log.Printf("Created new room: %s", roomID)
	return room
}
