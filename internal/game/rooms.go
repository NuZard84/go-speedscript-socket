package game

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/NuZard84/go-socket-speedscript/internal/constants"
	"github.com/NuZard84/go-socket-speedscript/internal/db"

	"github.com/NuZard84/go-socket-speedscript/internal/models"
)

type RoomManagerInterface interface {
	RemoveRoom(roomID string)
}

var CurrentManager RoomManagerInterface

func SetRoomManager(rm RoomManagerInterface) {
	CurrentManager = rm
}

// Room represents a game room where multiple players compete
type Room struct {
	ID                string
	Clients           map[string]*Client
	Text              string
	Status            string
	StartTime         *time.Time
	NextRank          int
	Mutex             sync.RWMutex
	StatsChan         chan models.FinalStatsMessage
	AdminRole         string
	LastWpmUpdateTime time.Time
	AdminUsername     string
	MaxCapacity       int
	TimeoutDuration   int // Duration in seconds
}

type AdminAction struct {
	Action      string      `json:"action"`
	Target      string      `json:"target"`
	RoomID      string      `json:"roomId"`
	MaxCapacity int         `json:"maxCapacity,omitempty"`
	Data        interface{} `json:"data,omitempty"`
}

func setTextFromDb() string {

	ctx := context.Background()
	sentence, err := db.GetRandomSentence(ctx)

	if err != nil {
		log.Printf("Error fetching random sentence: %v", err)
		return "This is a sample text"
	}
	return sentence.Story
}

func NewRoom(id string, adminUsername string, capacity int) *Room {
	log.Printf("Creating new room: %s", id)

	if adminUsername == "" {
		log.Printf("Creating new room: %s with no admin ", id)
	}
	if capacity <= 0 {
		capacity = constants.MaxmimumPlayers
	}

	text := setTextFromDb()

	return &Room{
		ID:                id,
		Clients:           make(map[string]*Client),
		Status:            constants.StatusWaiting,
		Text:              text,
		StatsChan:         make(chan models.FinalStatsMessage, constants.MaxmimumPlayers),
		AdminRole:         "spectator",
		LastWpmUpdateTime: time.Now(),
		AdminUsername:     adminUsername,
		MaxCapacity:       capacity,
		TimeoutDuration:   30, // Default timeout for competitive rooms
	}
}

// GAME STATE MANAGEMENT =>

// startGame initiates the game countdown and start sequence
func (room *Room) StartGame() {
	room.Mutex.Lock()
	room.Status = constants.StatusCountdown
	room.Mutex.Unlock()

	for i := 10; i > 0; i-- {
		room.BroadcastMessage(models.Message{
			Type: "countdown",
			Data: i,
		})
		time.Sleep(time.Second)
	}

	room.Mutex.Lock()
	now := time.Now()
	room.StartTime = &now
	room.Status = constants.StatusInProgress
	room.NextRank = 1
	room.Mutex.Unlock()

	room.BroadcastRoomState()
}

func (room *Room) handleGameFinished() {
	log.Printf("Game has finished in room %s", room.ID)

	// Start the goroutine to merge and broadcast stats
	go room.MergeAndBroadcastStats()

	room.BroadcastMessage(models.Message{
		Type: "game_finished",
		Data: map[string]interface{}{
			"message": "Game has finished",
			"status":  constants.StatusFinished,
		},
	})

	room.BroadcastRoomState()
}

// handleClientFinish processes a player finishing the game
func (room *Room) HandleClientFinish(client *Client) {
	room.Mutex.Lock()

	if room.Status != constants.StatusInProgress {
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

	finishEvent := models.Message{
		Type:   "user_finished",
		RoomID: room.ID,
		Data: map[string]interface{}{
			"rank":          client.Stats.Rank,
			"wpm":           wpm,
			"time":          now.Sub(*room.StartTime).Seconds(),
			"username":      client.Username,
			"finalPosition": currentPos,
			"duration":      room.TimeoutDuration,
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
		room.Status = constants.StatusFinished
	}

	room.Mutex.Unlock()

	room.BroadcastMessage(finishEvent)
	room.BroadcastRoomState()

	if allFinished {
		room.handleGameFinished()
	}
}

// handleTimeout processes the timeout message from the client
func (room *Room) HandleTimeout() {
	room.Mutex.Lock()

	if room.Status != constants.StatusInProgress {
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

	room.Status = constants.StatusFinished
	room.Mutex.Unlock()

	timeoutMsg := models.Message{
		Type: "game_timeout",
		Data: map[string]interface{}{
			"message":  "Game time limit reached",
			"status":   constants.StatusFinished,
			"duration": room.TimeoutDuration,
		},
	}

	room.BroadcastMessage(timeoutMsg)

	room.handleGameFinished()
}

// CLIENT MANAGEMENT =>

// AddClient adds a new client to the room
func (room *Room) AddClient(client *Client) error {
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	room.Text = setTextFromDb()

	if room.Status != constants.StatusWaiting {
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

	go room.BroadcastRoomState()
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

	go room.BroadcastRoomState()

	if len(room.Clients) < constants.MinPlayersToStart && room.Status != constants.StatusWaiting {
		room.Status = constants.StatusWaiting
		room.StartTime = nil

		for _, c := range room.Clients {
			c.Mu.Lock()
			c.Stats.IsReady = false
			c.Stats.CurrentPosition = 0
			c.Stats.WPM = 0
			c.Mu.Unlock()
		}

		go room.BroadcastMessage(models.Message{
			Type: constants.StatusReseting,
			Data: "Game reset: Not enough players",
		})
	}

	if len(room.Clients) == 0 {
		go CurrentManager.RemoveRoom(room.ID)
	}
}

// COMMINICATIONS

// BroadcastMessage sends a message to all clients in the room
func (room *Room) BroadcastMessage(msg models.Message) {
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

// broadcastRoomState sends current room state to all clients
func (room *Room) BroadcastRoomState() {
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
		TimeoutDuration int                     `json:"timeoutDuration"`
	}{
		Status:          room.Status,
		Players:         make(map[string]*PlayerStats),
		Text:            room.Text,
		StartTime:       room.StartTime,
		TotalCharacters: textLength,
		MaxCapacity:     room.MaxCapacity,
		CurrentPlayers:  len(room.Clients),
		TimeoutDuration: room.TimeoutDuration,
	}

	for username, client := range room.Clients {
		client.Mu.RLock()
		stats := &PlayerStats{
			IsReady:         client.Stats.IsReady,
			CurrentPosition: client.Stats.CurrentPosition,
			WPM:             math.Round(client.Stats.WPM*100) / 100,
			Rank:            client.Stats.Rank,
			HighestWpm:      client.UserProfile.HighestWpm,
		}
		if client.Stats.FinishTime != nil {
			finishTime := *client.Stats.FinishTime
			stats.FinishTime = &finishTime
		}
		state.Players[username] = stats
		client.Mu.RUnlock()
	}
	room.Mutex.RUnlock()

	room.BroadcastMessage(models.Message{
		Type:      "room_state",
		Data:      state,
		Time:      time.Now(),
		RoomID:    room.ID,
		RoomAdmin: room.AdminUsername,
		AdminRole: room.AdminRole,
	})
}

// STATS MANAGEMENT

func (room *Room) HandleFinalStats(client *Client, msg models.Message) {
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	log.Printf("Processing final stats for client: %s", client.Username)

	if room.Status != constants.StatusFinished {
		log.Printf("Game not finished, stats not accepted")
		return
	}

	if data, ok := msg.Data.(map[string]interface{}); ok {
		room.StatsChan <- models.FinalStatsMessage{
			Username: client.Username,
			Stats:    data,
		}
	}
}

func (room *Room) MergeAndBroadcastStats() {
	var allStats models.FinalGameStats
	allStats.RoomID = room.ID

	// Create a map to store stats for each player
	playerStatsMap := make(map[string]models.FinalPlayerStats)

	// Use a loop to wait for stats with a timeout
	timeout := time.After(10 * time.Second)
	statsReceived := 0
	totalClients := len(room.Clients)
	for statsReceived < totalClients {
		select {
		case statsMsg := <-room.StatsChan:
			if stats, ok := statsMsg.Stats["stats"].(map[string]interface{}); ok {
				playerStat := models.FinalPlayerStats{
					Username: statsMsg.Username,
					RoomID:   room.ID,
				}
				// Parse WPM safely
				if wpmStr, ok := stats["wpm"].(string); ok {
					if finalWPM, err := strconv.ParseFloat(wpmStr, 64); err == nil {
						playerStat.FinalWPM = finalWPM
					}
				}
				// Parse timeStats safely
				if timeStats, ok := stats["timeStats"].([]interface{}); ok {
					for _, stat := range timeStats {
						if statMap, ok := stat.(map[string]interface{}); ok {
							timeVal, timeOk := statMap["time"].(float64)
							wpmVal, wpmOk := statMap["wpm"].(float64)
							if timeOk && wpmOk {
								playerStat.Stats = append(playerStat.Stats, models.PlayerTimeStats{
									Time: timeVal,
									WPM:  wpmVal,
								})
							}
						}
					}
				}
				playerStatsMap[statsMsg.Username] = playerStat
			}
			statsReceived++
		case <-timeout:
			log.Printf("Timeout reached waiting for stats; broadcasting partial stats")
			// Break out of the loop to broadcast whatever we have
			goto BroadcastStats
		}
	}

BroadcastStats:
	// Merge the gathered stats with client finish times
	room.Mutex.RLock()
	for _, client := range room.Clients {
		client.Mu.RLock()
		if stats, ok := playerStatsMap[client.Username]; ok && client.Stats.FinishTime != nil {
			stats.Rank = client.Stats.Rank
			stats.FinishTime = client.Stats.FinishTime.Sub(*room.StartTime).Seconds()
			allStats.Players = append(allStats.Players, stats)
		}
		client.Mu.RUnlock()
	}
	room.Mutex.RUnlock()

	broadcastMsg := models.Message{
		Type: "ws_final_stat",
		Data: map[string]interface{}{
			"players": allStats.Players,
			"roomId":  allStats.RoomID,
		},
		Time: time.Now(),
	}

	room.BroadcastMessage(broadcastMsg)
	log.Printf("Final stats broadcast completed successfully (partial stats may have been broadcast)")
}

// VALIDATION

func (room *Room) ValidateAllPlayersReady() bool {
	room.Mutex.RLock()
	defer room.Mutex.RUnlock()

	if room.AdminUsername != "" {
		room.Clients[room.AdminUsername].Mu.RLock()
		defer room.Clients[room.AdminUsername].Mu.RUnlock()
		if !room.Clients[room.AdminUsername].Stats.IsReady {
			return false
		}
	} else {
		if len(room.Clients) < constants.MinPlayersToStart {
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
	}

	return true
}

// Add method to check if a user is admin
func (room *Room) IsAdmin(username string) bool {
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	return username == room.AdminUsername && room.AdminUsername != ""
}

// Add method to handle admin actions
func (room *Room) HandleAdminAction(action AdminAction, client *Client) error {
	if !room.IsAdmin(client.Username) {
		return fmt.Errorf("unauthorized: only admin can perform this action")
	}

	var err error

	switch action.Action {
	case constants.AdminActionKick:
		err = room.KickPlayer(action.Target, client)
	case constants.AdminActionUpdateCapacity:
		err = room.UpdateCapacity(action.MaxCapacity)
	case constants.AdminActionUpdateTimeout:
		if duration, ok := action.Data.(float64); ok {
			err = room.UpdateTimeout(int(duration))
		} else {
			err = fmt.Errorf("invalid timeout duration")
		}
	default:
		err = fmt.Errorf("unknown admin action: %s", action.Action)
	}

	if err != nil {
		// Send error message to client
		errorMsg := models.Message{
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
func (room *Room) UpdateCapacity(newCapacity int) error {
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	if newCapacity < len(room.Clients) {
		return fmt.Errorf("cannot set capacity below current player count")
	}

	if newCapacity > 50 {
		return fmt.Errorf("room capacity cannot exceed 50 players")
	}

	if newCapacity <= 0 {
		return fmt.Errorf("invalid capacity value")
	}

	/* No messages are lost
	No goroutines are left hanging
	Only happens when room is waiting (safe state)
	*/

	if room.Status == constants.StatusWaiting {
		// Safely close and recreate the channel
		if room.StatsChan != nil {
			// Drain the existing channel first
			for len(room.StatsChan) > 0 {
				<-room.StatsChan
			}
			close(room.StatsChan)
		}
		room.StatsChan = make(chan models.FinalStatsMessage, newCapacity)
	}

	room.MaxCapacity = newCapacity
	log.Printf("Room %s capacity updated to %d", room.ID, room.MaxCapacity)

	// Use a separate goroutine for broadcasting to prevent deadlock
	go func() {
		room.BroadcastMessage(models.Message{
			Type: "room_capacity_updated",
			Data: map[string]interface{}{
				"maxCapacity": newCapacity,
			},
		})
		room.BroadcastRoomState()
	}()

	return nil
}

// kick players
func (room *Room) KickPlayer(targetUsername string, adminClient *Client) error {
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	if targetUsername == room.AdminUsername {
		return fmt.Errorf("cannot kick the admin")
	}

	targetClient, exists := room.Clients[targetUsername]
	if !exists {
		return fmt.Errorf("player %s not found in room", targetUsername)
	}

	kickMsg := models.Message{
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
	notifyMsg := models.Message{
		Type: "player_kicked",
		Data: map[string]interface{}{
			"kicked_player": targetUsername,
			"by_admin":      adminClient.Username,
		},
	}

	go room.BroadcastMessage(notifyMsg)

	return nil
}

func (room *Room) SendWpmUpdatesToAdmin() {
	room.Mutex.RLock()
	defer room.Mutex.RUnlock()

	if room.Status != constants.StatusInProgress ||
		room.AdminUsername == "" ||
		room.AdminRole != "spectator" {
		return
	}

	admin, exists := room.Clients[room.AdminUsername]
	if !exists {
		return
	}

	playerWpmData := make(map[string]float64, len(room.Clients)-1)
	for username, client := range room.Clients {
		if username == room.AdminUsername {
			continue
		}
		client.Mu.RLock()
		playerWpmData[username] = client.Stats.WPM
		client.Mu.RUnlock()
	}

	room.LastWpmUpdateTime = time.Now()

	// Create a slice of player data for sorting
	type playerData struct {
		username string
		wpm      float64
	}
	players := make([]playerData, 0, len(playerWpmData))
	for username, wpm := range playerWpmData {
		players = append(players, playerData{username, wpm})
	}

	// Sort players by WPM in descending order
	sort.Slice(players, func(i, j int) bool {
		return players[i].wpm > players[j].wpm
	})

	// Create rank map for quick lookup
	rankMap := make(map[string]int)
	for i, player := range players {
		rankMap[player.username] = i + 1
	}

	// Send rank updates to individual players
	for username, client := range room.Clients {
		if username == room.AdminUsername {
			continue
		}
		fmt.Println("ranking send to ", username)
		if rank, exists := rankMap[username]; exists {
			rankUpdateMsg := models.Message{
				Type: "player_rank_update",
				Data: map[string]interface{}{
					"rank":         rank,
					"totalPlayers": len(players),
					"timestamp":    time.Now(),
				},
			}

			client.WriteMu.Lock()
			client.Conn.WriteJSON(rankUpdateMsg)
			client.WriteMu.Unlock()
		}
	}

	// Send WPM updates to admin
	playerWpmList := make([]map[string]interface{}, 0, len(playerWpmData))
	for username, wpm := range playerWpmData {
		playerWpmList = append(playerWpmList, map[string]interface{}{
			"username": username,
			"wpm":      wpm,
		})
	}

	wpmUpdateMsg := models.Message{
		Type: "admin_wpm_update",
		Data: map[string]interface{}{
			"playerWpmData": playerWpmList,
			"timestamp":     time.Now(),
		},
	}

	admin.WriteMu.Lock()
	defer admin.WriteMu.Unlock()

	admin.Conn.WriteJSON(wpmUpdateMsg)
}

func (room *Room) HandleClientWpmUpdate(client *Client, wpm float64) {

	client.Mu.Lock()
	client.Stats.WPM = wpm
	client.Mu.Unlock()

	room.Mutex.RLock()
	isGameInProgress := room.Status == constants.StatusInProgress
	isAdminSpectator := room.AdminRole == "spectator" && room.AdminUsername != ""
	shouldUpdateAdmin := time.Since(room.LastWpmUpdateTime) > 1500*time.Millisecond
	room.Mutex.RUnlock()

	if isGameInProgress && isAdminSpectator && shouldUpdateAdmin {
		go room.SendWpmUpdatesToAdmin()
	}

}

func (room *Room) HandleResetRoomState() error {
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	if room.Status != constants.StatusFinished {
		return fmt.Errorf("cannot reset; the game is not finished yet")
	}

	room.Status = constants.StatusWaiting
	room.StartTime = nil
	room.NextRank = 1

	room.Text = setTextFromDb()

	for _, client := range room.Clients {
		client.Mu.Lock()
		client.Stats = &PlayerStats{
			IsReady:         false,
			CurrentPosition: 0,
			WPM:             0.0,
			FinishTime:      nil,
			Rank:            0,
			HighestWpm:      client.UserProfile.HighestWpm,
		}
		client.Mu.Unlock()
	}

	if room.StatsChan != nil {
		close(room.StatsChan)
	}
	room.StatsChan = make(chan models.FinalStatsMessage, room.MaxCapacity)

	log.Printf("Room %s successfully reset to waiting state", room.ID)
	return nil
}

// update timeout duration
func (room *Room) UpdateTimeout(newDuration int) error {
	room.Mutex.Lock()
	defer room.Mutex.Unlock()

	// Validate duration
	validDurations := []int{10, 30, 60, 120}
	isValid := false
	for _, duration := range validDurations {
		if newDuration == duration {
			isValid = true
			break
		}
	}

	if !isValid {
		return fmt.Errorf("invalid timeout duration. Must be one of: 10, 30, 60, 120 seconds")
	}

	if room.Status != constants.StatusWaiting {
		return fmt.Errorf("cannot change timeout while game is in progress")
	}

	room.TimeoutDuration = newDuration
	log.Printf("Room %s timeout updated to %d seconds", room.ID, room.TimeoutDuration)

	// Broadcast the timeout update to all clients in the room
	go room.BroadcastMessage(models.Message{
		Type: "timeout_updated",
		Data: map[string]interface{}{
			"duration": newDuration,
		},
	})

	return nil
}
