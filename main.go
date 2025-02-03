package main

import (
	"context"
	"encoding/json"
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

	AdminActionKick           = "kick"
	AdminActionUpdateCapacity = "update_capacity"
)

// Client represents a connected player with their connection and game stats
type Client struct {
	Conn        *websocket.Conn
	Username    string
	Room        *Room
	Stats       *PlayerStats
	UserProfile UserProfile
	Mu          sync.RWMutex
	WriteMu     sync.Mutex
}

type UserProfile struct {
	DailyHighestWpm int `bson:"dailyHighestWpm"`
	HighestWpm      int `bson:"highestWpm"`
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
	ID            string
	Clients       map[string]*Client
	Text          string
	Status        string
	StartTime     *time.Time
	NextRank      int
	Mutex         sync.RWMutex
	StatsChan     chan FinalStatsMessage
	AdminUsername string
	MaxCapacity   int
}

type AdminAction struct {
	Action      string `json:"action"`
	Target      string `json:"target"`
	RoomID      string `json:"roomId"`
	MaxCapacity int    `json:"maxCapacity,omitempty"`
}

// Message defines the structure for WebSocket communication
type Message struct {
	Type            string      `json:"type"`
	Username        string      `json:"username"`
	RoomID          string      `json:"room_id"`
	Data            interface{} `json:"data"`
	Time            time.Time   `json:"timestamp"`
	Text            string      `json:"text"`
	RoomAdmin       string      `json:"room_admin"`
	TotalCharacters int         `json:"totalCharacters,omitempty"`
}

// RoomManager handles the creation and management of game rooms
type RoomManager struct {
	Rooms        map[string]*Room
	Mutex        sync.RWMutex
	MaxRooms     int
	ActiveRooms  int
	WaitingRooms []*Room
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

func setTextFromDb() string { // -done

	ctx := context.Background()
	sentence, err := db.GetRandomSentence(ctx)

	if err != nil {
		log.Printf("Error fetching random sentence: %v", err)
		return "This is a sample text"
	}
	return sentence.Story
}

func setProfileFromDb(username string) *UserProfile {
	ctx := context.Background()
	userProfile, err := db.GetUserProfile(ctx, username)

	if err != nil {
		log.Printf("Error fetching User profile for %s: %v", username, err)
		return &UserProfile{
			DailyHighestWpm: 0,
			HighestWpm:      0,
		}
	}

	if userProfile == nil {
		log.Printf("No profile found for user %s", username)
		return &UserProfile{
			DailyHighestWpm: 0,
			HighestWpm:      0,
		}
	}

	return &UserProfile{
		DailyHighestWpm: userProfile.DailyHighestWpm,
		HighestWpm:      userProfile.HighestWpm,
	}
}

// NewRoomManager creates a new room manager instance - done
func NewRoomManager(maxRooms int) *RoomManager {
	log.Printf("Creating new room manager with max rooms: %d", maxRooms)
	return &RoomManager{
		Rooms:        make(map[string]*Room),
		MaxRooms:     maxRooms,
		WaitingRooms: make([]*Room, 0),
	}
}

// NewClient creates a new client instance with initialized stats - done
func NewClient(conn *websocket.Conn, username string) *Client {
	log.Printf("New client connected: %s", username)
	return &Client{
		Conn:     conn,
		Username: username,
		Stats: &PlayerStats{
			CurrentPosition: 0,
			WPM:             0,
		},
		UserProfile: *setProfileFromDb("hett84"),
	}
}

// NewRoom creates a new game room with the given ID - done
func NewRoom(id string, adminUsername string, capacity int) *Room {
	if adminUsername == "" {
		log.Printf("Creating new room: %s with no admin ", id)
	}

	if capacity <= 0 {
		capacity = MaxmimumPlayers
	}

	log.Printf("Creating new room: %s with admin: %s", id, adminUsername)
	text := setTextFromDb()

	return &Room{
		ID:            id,
		Clients:       make(map[string]*Client),
		Status:        StatusWaiting,
		Text:          text,
		StatsChan:     make(chan FinalStatsMessage, MaxmimumPlayers), // Initialize the channel
		AdminUsername: adminUsername,
		MaxCapacity:   capacity,
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

// Add this middleware function at the top level
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
	http.HandleFunc("/ws/room", handleWebSocket)
	http.HandleFunc("/api/create-room", enableCORS(handleCreateRoom))
	http.HandleFunc("/api/check-room", enableCORS(handleCheckRoom))
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

	var room *Room

	if roomID != "" {
		existingRoom, err := roomManager.GetRoom(roomID)
		if err != nil {
			conn.WriteJSON(Message{
				Type: "error",
				Data: "Room not found",
			})
			conn.Close()
			return
		}
		room = existingRoom
	} else {
		room = roomManager.FindOrCreateRoom()

	}

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

// generate random roomIDs - done
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
		case "admin_action":
			var adminAction AdminAction
			if data, ok := msg.Data.(map[string]interface{}); ok {
				action := data["action"].(string)
				maxCap, _ := data["maxCapacity"].(float64)
				if action == AdminActionKick {
					adminAction = AdminAction{
						Action: data["action"].(string),
						Target: data["target"].(string),
						RoomID: data["room_id"].(string),
					}
				}
				if action == AdminActionUpdateCapacity {
					adminAction = AdminAction{
						Action:      data["action"].(string),
						MaxCapacity: int(maxCap),
						RoomID:      data["room_id"].(string),
					}
				}

				if err := room.handleAdminAction(adminAction, client); err != nil {
					client.WriteMu.Lock()
					client.Conn.WriteJSON(Message{
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
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

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
	room.Mutex.RLock()
	defer room.Mutex.RUnlock()

	for _, client := range room.Clients {
		client.Mu.RLock()
		if stats, ok := playerStatsMap[client.Username]; ok {
			stats.Rank = client.Stats.Rank
			stats.FinishTime = client.Stats.FinishTime.Sub(*room.StartTime).Seconds()
			allStats.Players = append(allStats.Players, stats)
		}
		client.Mu.RUnlock()
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

	client.Mu.Lock()
	client.Stats.IsReady = readyState
	client.Mu.Unlock()

	if room.validateAllPlayersReady() {
		go room.startGame()
	}

	go room.broadcastRoomState()
}

func (room *Room) validateAllPlayersReady() bool {
	room.Mutex.RLock()
	defer room.Mutex.RUnlock()

	if len(room.Clients) < MinPlayersToStart {
		return false
	}

	for _, client := range room.Clients {
		client.Mu.RLock()
		if !client.Stats.IsReady {
			client.Mu.RUnlock()
			return false
		}
		client.Mu.RUnlock()
	}

	return true
}

// handleProgress updates player progress during the game
func handleProgress(room *Room, client *Client, msg Message) {
	room.Mutex.RLock()
	if room.Status != StatusInProgress {
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
		room.handleClientFinish(client)
	} else {
		room.broadcastRoomState()
	}
}

// handlePing responds to client ping messages
func handlePing(client *Client) {
	client.Mu.Lock()
	defer client.Mu.Unlock()

	client.Conn.WriteJSON(Message{
		Type: "pong",
		Data: time.Now(),
	})
}

// startGame initiates the game countdown and start sequence
func (room *Room) startGame() {
	room.Mutex.Lock()
	room.Status = StatusCountdown
	room.Mutex.Unlock()

	for i := 3; i > 0; i-- {
		room.BroadcastMessage(Message{
			Type: "countdown",
			Data: i,
		})
		time.Sleep(time.Second)
	}

	room.Mutex.Lock()
	now := time.Now()
	room.StartTime = &now
	room.Status = StatusInProgress
	room.NextRank = 1
	room.Mutex.Unlock()

	room.broadcastRoomState()
}

// handleClientFinish processes a player finishing the game
func (room *Room) handleClientFinish(client *Client) {
	room.Mutex.Lock()

	if room.Status != StatusInProgress {
		room.Mutex.Unlock()
		return
	}

	client.Mu.RLock()
	if client.Stats.FinishTime != nil {
		client.Mu.RUnlock()
		room.Mutex.Unlock()
		return
	}
	currentPos := client.Stats.CurrentPosition
	client.Mu.RUnlock()

	now := time.Now()

	client.Mu.Lock()
	client.Stats.FinishTime = &now
	client.Stats.CurrentPosition = currentPos
	client.Stats.Rank = room.NextRank
	wpm := client.Stats.WPM
	log.Printf("user: %s at finish_state in room: %s with position: %d", client.Username, room.ID, currentPos)
	client.Mu.Unlock()

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
		c.Mu.RLock()
		if c.Stats.FinishTime == nil {
			allFinished = false
		}
		c.Mu.RUnlock()
	}

	if allFinished {
		log.Printf("All players have finished in room %s", room.ID)
		room.Status = StatusFinished
	}

	room.Mutex.Unlock()

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
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	if room.Status != StatusWaiting {
		return fmt.Errorf("this room is already in busy state")
	}

	if _, ok := room.Clients[client.Username]; ok {
		return fmt.Errorf("this username is already taken")
	}

	if len(room.Clients) >= room.MaxCapacity {
		return fmt.Errorf("room has reached maximum capacity of %d players", room.MaxCapacity)
	}

	room.Clients[client.Username] = client
	client.Room = room

	go room.broadcastRoomState()
	return nil
}

// RemoveClient removes a client from the room
func (room *Room) RemoveClient(client *Client) {
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	if client == nil {
		return
	}

	client.Mu.Lock()
	if client.Conn != nil {
		client.Conn.Close()
	}
	client.Mu.Unlock()

	delete(room.Clients, client.Username)

	go room.broadcastRoomState()

	if len(room.Clients) < MinPlayersToStart && room.Status != StatusWaiting {
		room.Status = StatusWaiting
		room.StartTime = nil

		for _, c := range room.Clients {
			c.Mu.Lock()
			c.Stats.IsReady = false
			c.Stats.CurrentPosition = 0
			c.Stats.WPM = 0
			c.Mu.Unlock()
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
	room.Mutex.RLock()
	textLength := len(room.Text)
	state := struct {
		Status          string                  `json:"status"`
		Players         map[string]*PlayerStats `json:"players"`
		Text            string                  `json:"text,omitempty"`
		StartTime       *time.Time              `json:"startTime,omitempty"`
		TotalCharacters int                     `json:"totalCharacters"`
		MaxCapacity     int                     `json:"maxCapacity"`
		CurrentPlayers  int                     `json:"currentPlayers"`
	}{
		Status:          room.Status,
		Players:         make(map[string]*PlayerStats),
		Text:            room.Text,
		StartTime:       room.StartTime,
		TotalCharacters: textLength,
		MaxCapacity:     room.MaxCapacity,
		CurrentPlayers:  len(room.Clients),
	}

	for username, client := range room.Clients {
		client.Mu.RLock()
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
		client.Mu.RUnlock()
	}
	room.Mutex.RUnlock()

	room.BroadcastMessage(Message{
		Type:      "room_state",
		Data:      state,
		Time:      time.Now(),
		RoomID:    room.ID,
		RoomAdmin: room.AdminUsername,
		Text:      room.Text,
	})
}

// BroadcastMessage sends a message to all clients in the room
func (room *Room) BroadcastMessage(msg Message) {
	room.Mutex.RLock()
	defer room.Mutex.RUnlock()

	log.Printf("Starting broadcast - Message Type: %s, Room ID: %s, Clients: %d", msg.Type, room.ID, len(room.Clients))

	var wg sync.WaitGroup
	errorsChan := make(chan error, len(room.Clients))

	for username, client := range room.Clients {
		wg.Add(1)
		go func(c *Client, name string) {
			defer wg.Done()

			c.WriteMu.Lock()
			defer c.WriteMu.Unlock()

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
	room.Mutex.Lock()

	if room.Status != StatusInProgress {
		log.Printf("Timeout called but game not in progress. Current status: %s", room.Status)
		room.Mutex.Unlock()
		return
	}

	log.Printf("Game timeout processing - Room ID: %s", room.ID)

	// Mark unfinished players
	for username, client := range room.Clients {
		client.Mu.Lock()
		if client.Stats.FinishTime == nil {
			now := time.Now()
			client.Stats.FinishTime = &now
			client.Stats.Rank = room.NextRank
			room.NextRank++
			log.Printf("Timeout: Player %s marked with rank %d", username, client.Stats.Rank)
		}
		client.Mu.Unlock()
	}

	room.Status = StatusFinished
	room.Mutex.Unlock()

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
func (rm *RoomManager) RemoveRoom(roomID string) { //-done
	rm.Mutex.Lock()
	defer rm.Mutex.Unlock()

	if _, ok := rm.Rooms[roomID]; !ok {
		log.Printf("Room does not exist!")
		return
	}

	delete(rm.Rooms, roomID)
	rm.ActiveRooms--

	// Remove from waitingRooms
	for i, room := range rm.WaitingRooms {
		if room.ID == roomID {
			rm.WaitingRooms = append(rm.WaitingRooms[:i], rm.WaitingRooms[i+1:]...)
			break
		}
	}

	log.Printf("Room removed: %s, Active rooms: %d", roomID, rm.ActiveRooms)
}

func (rm *RoomManager) FindOrCreateRoom() *Room { //-done

	rm.Mutex.Lock()
	defer rm.Mutex.Unlock()

	//Check if any room have slots
	for _, room := range rm.WaitingRooms {
		if len(room.Clients) < MaxmimumPlayers {
			if room.Status == StatusWaiting {
				return room
			}
		}
	}

	//If no slots are found, Create a new one
	roomID := generateRoomID()
	room := NewRoom(roomID, "", MaxmimumPlayers)
	rm.Rooms[roomID] = room
	rm.WaitingRooms = append(rm.WaitingRooms, room)
	rm.ActiveRooms++

	return room

}

// getOrCreateRoom retrieves an existing room or creates a new one - done
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

func (rm *RoomManager) CreateCustomRoom(adminUsername string, capacity int) *Room {
	rm.Mutex.Lock()
	defer rm.Mutex.Unlock()

	roomID := generateRoomID()
	room := NewRoom(roomID, adminUsername, capacity)
	rm.Rooms[roomID] = room
	rm.ActiveRooms++

	log.Printf("Created custom room: %s", roomID)
	return room
}

func (rm *RoomManager) GetRoom(RoomID string) (*Room, error) {

	rm.Mutex.Lock()
	defer rm.Mutex.Unlock()

	room, exist := rm.Rooms[RoomID]
	if !exist {
		return nil, fmt.Errorf("room %s not found", RoomID)
	}

	return room, nil
}

//API for create and check a room

func handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var reqBody struct {
		Username    string `json:"username"`
		MaxCapacity int    `json:"maxCapacity,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	capacity := reqBody.MaxCapacity
	if capacity <= 0 {
		capacity = MaxmimumPlayers
	}

	room := roomManager.CreateCustomRoom(reqBody.Username, capacity)

	log.Printf("Room %s created by admin: %s", room.ID, reqBody.Username)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"room_id": room.ID,
		"status":  "created",
		"admin":   reqBody.Username,
	})
}

func handleCheckRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	roomID := r.URL.Query().Get("room_id")
	if roomID == "" {
		http.Error(w, "Missing room_id", http.StatusBadRequest)
		return
	}

	_, err := roomManager.GetRoom(roomID)
	w.Header().Set("Content-Type", "application/json")

	if err != nil {
		json.NewEncoder(w).Encode(map[string]bool{
			"exists": false,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{
		"exists": true,
	})

}

// Add method to check if a user is admin
func (room *Room) isAdmin(username string) bool {
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	return username == room.AdminUsername && room.AdminUsername != ""
}

// Add method to handle admin actions
func (room *Room) handleAdminAction(action AdminAction, client *Client) error {
	if !room.isAdmin(client.Username) {
		return fmt.Errorf("unauthorized: only admin can perform this action")
	}

	var err error
	switch action.Action {
	case AdminActionKick:
		err = room.kickPlayer(action.Target, client)
	case AdminActionUpdateCapacity:
		err = room.updateCapacity(action.MaxCapacity)
	default:
		err = fmt.Errorf("unknown admin action: %s", action.Action)
	}

	if err != nil {
		// Send error message to client
		errorMsg := Message{
			Type: "error",
			Data: err.Error(),
		}
		client.WriteMu.Lock()
		client.Conn.WriteJSON(errorMsg)
		client.WriteMu.Unlock()
	}

	return err
}

// update capacity
func (room *Room) updateCapacity(newCapacity int) error {
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	if newCapacity < len(room.Clients) {
		return fmt.Errorf("cannot set capacity below current player count")
	}

	if newCapacity > 50 {
		return fmt.Errorf("room capacity must be at least 50")
	}

	if newCapacity <= 0 {
		return fmt.Errorf("invalid capacity value")
	}

	/* No messages are lost
	No goroutines are left hanging
	Only happens when room is waiting (safe state)
	*/
	if room.Status == StatusWaiting {
		// Safely close and recreate the channel
		if room.StatsChan != nil {
			// Drain the existing channel first
			for len(room.StatsChan) > 0 {
				<-room.StatsChan
			}
			close(room.StatsChan)
		}
		room.StatsChan = make(chan FinalStatsMessage, newCapacity)
	}

	room.MaxCapacity = newCapacity

	log.Printf("Room %s capacity updated to %d", room.ID, room.MaxCapacity)

	// Use a separate goroutine for broadcasting to prevent deadlock
	go func() {
		room.BroadcastMessage(Message{
			Type: "room_capacity_updated",
			Data: map[string]interface{}{
				"maxCapacity": newCapacity,
			},
		})

		room.broadcastRoomState()
	}()

	return nil
}

// kick players
func (room *Room) kickPlayer(targetUsername string, adminClient *Client) error {
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	if targetUsername == room.AdminUsername {
		return fmt.Errorf("cannot kick the admin")
	}

	targetClient, exists := room.Clients[targetUsername]
	if !exists {
		return fmt.Errorf("player %s not found in room", targetUsername)
	}

	kickMsg := Message{
		Type: "you_are_kicked",
		Data: map[string]interface{}{
			"message": "You have been kicked from the room",
			"by":      adminClient.Username,
		},
	}

	targetClient.WriteMu.Lock()
	err := targetClient.Conn.WriteJSON(kickMsg)
	defer targetClient.WriteMu.Unlock()

	log.Printf("player %s has been kicked by admin %s", targetUsername, adminClient.Username)

	if err != nil {
		log.Printf("Error notifying kicked player: %v", err)
	}

	go room.RemoveClient(targetClient)

	notifyMsg := Message{
		Type: "player_kicked",
		Data: map[string]interface{}{
			"kicked_player": targetUsername,
			"by_admin":      adminClient.Username,
		},
	}

	go room.BroadcastMessage(notifyMsg)

	return nil
}
