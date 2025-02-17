package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/NuZard84/go-socket-speedscript/internal/constants"
	"github.com/NuZard84/go-socket-speedscript/internal/models"
)

func HandleCreateRoom(w http.ResponseWriter, r *http.Request) {

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
		capacity = constants.MaxmimumPlayers
	}

	room := RoomManager.CreateCustomRoom(reqBody.Username, capacity)

	log.Printf("Room %s created by user: %s", room.ID, reqBody.Username)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"room_id": room.ID,
		"status":  "created",
		"admin":   reqBody.Username,
	})
}

func HandleCheckRoom(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	roomID := r.URL.Query().Get("room_id")

	if roomID == "" {
		http.Error(w, "Missing room_id", http.StatusBadRequest)
		return
	}

	_, err := RoomManager.GetRoom(roomID, true)

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

func HandleAdminRoleChange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var reqBody struct {
		Username  string `json:"username"`
		RoomID    string `json:"room_id"`
		AdminRole string `json:"admin_role"`
	}

	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	room, err := RoomManager.GetRoom(reqBody.RoomID, true)
	if err != nil {
		http.Error(w, "Room not found", http.StatusNotFound)
		return
	}

	if !room.IsAdmin(reqBody.Username) {
		http.Error(w, "Unauthorized: only admin can change roles", http.StatusUnauthorized)
		return
	}

	if reqBody.AdminRole != "spectator" && reqBody.AdminRole != "player" {
		http.Error(w, "Invalid role: must be 'spectator' or 'player'", http.StatusBadRequest)
		return
	}

	room.Mutex.Lock()
	previousRole := room.AdminRole
	room.AdminRole = reqBody.AdminRole
	room.Mutex.Unlock()

	room.BroadcastMessage(models.Message{
		Type: "admin_role_changed",
		Data: map[string]interface{}{
			"role":         reqBody.AdminRole,
			"previousRole": previousRole,
		},
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"role":   reqBody.AdminRole,
	})

}

func HandleTestAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "test successful",
		"status":  "running",
	})
}
