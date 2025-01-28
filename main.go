package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/NuZard84/go-socket-speedscript/db"
	"github.com/google/uuid"
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
	MaxmimumPlayers   = 4
	CountdownDuration = 3 * time.Second
)

// Client represents a connected player with their connection and game stats
type Client struct {
	Conn     *websocket.Conn
	Username string
	Room     *Room
	Stats    *PlayerStats
	mu       sync.RWMutex
	writeMu  sync.Mutex
}

// PlayerStats tracks individual player performance during the game
type PlayerStats struct {
	IsReady         bool       `json:"isReady"`
	CurrentPosition int        `json:"currentPosition"`
	WPM             float64    `json:"wpm"`
	FinishTime      *time.Time `json:"finishTime,omitempty"`
	Rank            int        `json:"rank"`
}

type PlayerTimeStats struct {
	Time float64 `json:"time"`
	WPM  float64 `json:"wpm"`
}

type FinalPlayerStats struct {
	Username   string            `json:"username"`
	Stats      []PlayerTimeStats `json:"stats"`
	FinalWPM   float64           `json:"wpm"`
	Rank       int               `json:"rank"`
	FinishTime float64           `json:"finishTime"`
	RoomID     string            `json:"roomId"`
}

type FinalGameStats struct {
	Players []FinalPlayerStats `json:"players"`
	RoomID  string             `json:"roomId"`
}

type FinalStatsMessage struct {
	Username string
	Stats    map[string]interface{}
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
	StatsChan chan FinalStatsMessage
}

// Message defines the structure for WebSocket communication
type Message struct {
	Type            string      `json:"type"`
	Username        string      `json:"username"`
	RoomID          string      `json:"room_id"`
	Data            interface{} `json:"data"`
	Time            time.Time   `json:"timestamp"`
	Text            string      `json:"text"`
	TotalCharacters int         `json:"totalCharacters,omitempty"`
}

// RoomManager handles the creation and management of game rooms
type RoomManager struct {
	Rooms        map[string]*Room
	mutex        sync.RWMutex
	maxRooms     int
	activeRooms  int
	waitingRooms []*Room
}

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
		Rooms:        make(map[string]*Room),
		maxRooms:     maxRooms,
		waitingRooms: make([]*Room, 0),
	}
}

// NewClient creates a new client instance with initialized stats
func NewClient(conn *websocket.Conn, username string) *Client {
	log.Printf("New client connected: %s", username)
	return &Client{
		Conn:     conn,
		Username: username,
		Stats: &PlayerStats{
			CurrentPosition: 0,
			WPM:             0,
		},
	}
}

// NewRoom creates a new game room with the given ID
func NewRoom(id string) *Room {
	log.Printf("Creating new room: %s", id)
	text := setTextFromDb()

	return &Room{
		ID:        id,
		Clients:   make(map[string]*Client),
		Status:    StatusWaiting,
		Text:      text,
		StatsChan: make(chan FinalStatsMessage, MaxmimumPlayers), // Initialize the channel
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

	if username == "" {
		http.Error(w, "Missing username", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	client := NewClient(conn, username)
	room := roomManager.findOrCreateRoom()

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

// generate random roomIDs
func generateRoomID() string {
	uuidPart := uuid.New().String()[:8]
	roomID := "room_0x" + uuidPart
	return roomID
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
		case "final_stats":
			log.Printf("Received final_stats message: %+v", msg)
			room.handleFinalStats(client, msg)
		case "timeout":
			room.handleTimeout()
		}
	}
}

func (room *Room) handleFinalStats(client *Client, msg Message) {
	room.mutex.Lock()
	defer room.mutex.Unlock()

	log.Printf("Processing final stats for client: %s", client.Username)

	if room.Status != StatusFinished {
		log.Printf("Game not finished, stats not accepted")
		return
	}

	if data, ok := msg.Data.(map[string]interface{}); ok {
		room.StatsChan <- FinalStatsMessage{
			Username: client.Username,
			Stats:    data,
		}
	}
}

func (room *Room) mergeAndBroadcastStats() {
	var allStats FinalGameStats
	allStats.RoomID = room.ID

	// Create a map to store stats for each player
	playerStatsMap := make(map[string]FinalPlayerStats)

	// Wait for all clients to send their stats with a timeout
	timeout := time.After(10 * time.Second) // 10-second timeout
	for i := 0; i < len(room.Clients); i++ {
		select {
		case statsMsg := <-room.StatsChan:
			// Process stats
			if stats, ok := statsMsg.Stats["stats"].(map[string]interface{}); ok {
				playerStat := FinalPlayerStats{
					Username: statsMsg.Username,
					RoomID:   room.ID,
				}

				// Extract WPM and timeStats
				if wpm, ok := stats["wpm"].(string); ok {
					finalWPM, _ := strconv.ParseFloat(wpm, 64)
					playerStat.FinalWPM = finalWPM
				}

				if timeStats, ok := stats["timeStats"].([]interface{}); ok {
					for _, stat := range timeStats {
						if statMap, ok := stat.(map[string]interface{}); ok {
							playerStat.Stats = append(playerStat.Stats, PlayerTimeStats{
								Time: statMap["time"].(float64),
								WPM:  statMap["wpm"].(float64),
							})
						}
					}
				}

				playerStatsMap[statsMsg.Username] = playerStat
			}
		case <-timeout:
			log.Printf("Timeout waiting for stats from clients")
			return
		}
	}

	// Merge and broadcast stats
	room.mutex.RLock()
	defer room.mutex.RUnlock()

	for _, client := range room.Clients {
		client.mu.RLock()
		if stats, ok := playerStatsMap[client.Username]; ok {
			stats.Rank = client.Stats.Rank
			stats.FinishTime = client.Stats.FinishTime.Sub(*room.StartTime).Seconds()
			allStats.Players = append(allStats.Players, stats)
		}
		client.mu.RUnlock()
	}

	broadcastMsg := Message{
		Type: "ws_final_stat",
		Data: map[string]interface{}{
			"players": allStats.Players,
			"roomId":  allStats.RoomID,
		},
		Time: time.Now(),
	}

	room.BroadcastMessage(broadcastMsg)
	log.Printf("Final stats broadcast completed successfully")
}

// handleReadyState processes player ready status updates
func handleReadyState(room *Room, client *Client, msg Message) {
	readyState, ok := msg.Data.(bool)
	if !ok {
		log.Printf("Invalid ready state: %v", msg.Data)
		return
	}

	client.mu.Lock()
	client.Stats.IsReady = readyState
	client.mu.Unlock()

	if room.validateAllPlayersReady() {
		go room.startGame()
	}

	go room.broadcastRoomState()
}

func (room *Room) validateAllPlayersReady() bool {
	room.mutex.RLock()
	defer room.mutex.RUnlock()

	if len(room.Clients) < MinPlayersToStart {
		return false
	}

	for _, client := range room.Clients {
		client.mu.RLock()
		if !client.Stats.IsReady {
			client.mu.RUnlock()
			return false
		}
		client.mu.RUnlock()
	}

	return true
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
	currentPos := client.Stats.CurrentPosition
	client.mu.RUnlock()

	now := time.Now()

	client.mu.Lock()
	client.Stats.FinishTime = &now
	client.Stats.CurrentPosition = currentPos
	client.Stats.Rank = room.NextRank
	wpm := client.Stats.WPM
	log.Printf("user: %s at finish_state in room: %s with position: %d", client.Username, room.ID, currentPos)
	client.mu.Unlock()

	room.NextRank++

	finishEvent := Message{
		Type:   "user_finished",
		RoomID: room.ID,
		Data: map[string]interface{}{
			"rank":          client.Stats.Rank,
			"wpm":           wpm,
			"time":          now.Sub(*room.StartTime).Seconds(),
			"username":      client.Username,
			"finalPosition": currentPos,
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

	// Start the goroutine to merge and broadcast stats
	go room.mergeAndBroadcastStats()

	room.BroadcastMessage(Message{
		Type: "game_finished",
		Data: map[string]interface{}{
			"message": "Game has finished",
			"status":  StatusFinished,
		},
	})

	room.broadcastRoomState()
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

	if len(room.Clients) == MaxmimumPlayers {
		return fmt.Errorf("room is full")
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

	go room.broadcastRoomState()

	if len(room.Clients) < MinPlayersToStart && room.Status != StatusWaiting {
		room.Status = StatusWaiting
		room.StartTime = nil

		for _, c := range room.Clients {
			c.mu.Lock()
			c.Stats.IsReady = false
			c.Stats.CurrentPosition = 0
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

// broadcastRoomState sends current room state to all clients
func (room *Room) broadcastRoomState() {
	room.mutex.RLock()
	textLength := len(room.Text)
	state := struct {
		Status          string                  `json:"status"`
		Players         map[string]*PlayerStats `json:"players"`
		Text            string                  `json:"text,omitempty"`
		StartTime       *time.Time              `json:"startTime,omitempty"`
		TotalCharacters int                     `json:"totalCharacters"`
	}{
		Status:          room.Status,
		Players:         make(map[string]*PlayerStats),
		Text:            room.Text,
		StartTime:       room.StartTime,
		TotalCharacters: textLength,
	}

	for username, client := range room.Clients {
		client.mu.RLock()
		stats := &PlayerStats{
			IsReady:         client.Stats.IsReady,
			CurrentPosition: client.Stats.CurrentPosition,
			WPM:             math.Round(client.Stats.WPM*100) / 100,
			Rank:            client.Stats.Rank,
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

// BroadcastMessage sends a message to all clients in the room
func (room *Room) BroadcastMessage(msg Message) {
	room.mutex.RLock()
	defer room.mutex.RUnlock()

	log.Printf("Starting broadcast - Message Type: %s, Room ID: %s, Clients: %d", msg.Type, room.ID, len(room.Clients))

	var wg sync.WaitGroup
	errorsChan := make(chan error, len(room.Clients))

	for username, client := range room.Clients {
		wg.Add(1)
		go func(c *Client, name string) {
			defer wg.Done()

			c.writeMu.Lock()
			defer c.writeMu.Unlock()

			if c.Conn == nil {
				log.Printf("ERROR: Client %s connection is nil", name)
				errorsChan <- fmt.Errorf("client %s connection is nil", name)
				return
			}

			msg.Time = time.Now()
			msg.RoomID = room.ID

			log.Printf("Sending message type - %s to %s", msg.Type, name)

			done := make(chan error, 1)
			go func() {
				done <- c.Conn.WriteJSON(msg)
			}()

			select {
			case err := <-done:
				if err != nil {
					log.Printf("ERROR sending to %s: %v", name, err)
					errorsChan <- fmt.Errorf("failed to send to %s: %v", name, err)
				} else {
					log.Printf("Message sent successfully to %s", name)
				}
			case <-time.After(5 * time.Second):
				log.Printf("ERROR: Timeout sending to %s", name)
				errorsChan <- fmt.Errorf("timeout sending to %s", name)
			}
		}(client, username)

		//to avoid overwhelming the WebSocket connection
		time.Sleep(10 * time.Millisecond)
	}

	wg.Wait()
	close(errorsChan)

	var errList []error
	for err := range errorsChan {
		errList = append(errList, err)
	}

	if len(errList) > 0 {
		log.Printf("Broadcast errors: %v", errList)
	} else {
		log.Printf("Broadcast completed successfully - Message type %s ", msg.Type)
	}
}

// handleTimeout processes the timeout message from the client
func (room *Room) handleTimeout() {
	room.mutex.Lock()

	if room.Status != StatusInProgress {
		log.Printf("Timeout called but game not in progress. Current status: %s", room.Status)
		room.mutex.Unlock()
		return
	}

	log.Printf("Game timeout processing - Room ID: %s", room.ID)

	// Mark unfinished players
	for username, client := range room.Clients {
		client.mu.Lock()
		if client.Stats.FinishTime == nil {
			now := time.Now()
			client.Stats.FinishTime = &now
			client.Stats.Rank = room.NextRank
			room.NextRank++
			log.Printf("Timeout: Player %s marked with rank %d", username, client.Stats.Rank)
		}
		client.mu.Unlock()
	}

	room.Status = StatusFinished
	room.mutex.Unlock()

	timeoutMsg := Message{
		Type: "game_timeout",
		Data: map[string]interface{}{
			"message": "Game time limit reached",
			"status":  StatusFinished,
		},
	}

	room.BroadcastMessage(timeoutMsg)

	room.handleGameFinished()
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

	// Remove from waitingRooms
	for i, room := range rm.waitingRooms {
		if room.ID == roomID {
			rm.waitingRooms = append(rm.waitingRooms[:i], rm.waitingRooms[i+1:]...)
			break
		}
	}

	log.Printf("Room removed: %s, Active rooms: %d", roomID, rm.activeRooms)
}

func (rm *RoomManager) findOrCreateRoom() *Room {

	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	//Check if any room have slots
	for _, room := range rm.waitingRooms {
		if len(room.Clients) < MaxmimumPlayers {
			if room.Status == StatusWaiting {
				return room
			}
		}
	}

	//If no slots are found, Create a new one
	roomID := generateRoomID()
	room := NewRoom(roomID)
	rm.Rooms[roomID] = room
	rm.waitingRooms = append(rm.waitingRooms, room)
	rm.activeRooms++

	return room

}

// getOrCreateRoom retrieves an existing room or creates a new one
// func getOrCreateRoom(roomID string) *Room {
// 	roomManager.mutex.Lock()
// 	defer roomManager.mutex.Unlock()

// 	if room, ok := roomManager.Rooms[roomID]; ok {
// 		log.Printf("Room already exists: %s", roomID)
// 		return room
// 	}

// 	room := NewRoom(roomID)
// 	roomManager.Rooms[roomID] = room
// 	roomManager.activeRooms++
// 	log.Printf("Created new room: %s", roomID)
// 	return room
// }
