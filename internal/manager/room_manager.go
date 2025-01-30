package manager

import (
	"log"
	"sync"

	"github.com/NuZard84/go-socket-speedscript/internal/constants"
	"github.com/NuZard84/go-socket-speedscript/internal/game"

	"github.com/google/uuid"
)

// RoomManager handles the creation and management of game rooms
type RoomManager struct {
	Rooms        map[string]*game.Room
	Mutex        sync.RWMutex
	MaxRooms     int
	ActiveRooms  int
	WaitingRooms []*game.Room
}

func generateRoomID() string {
	uuidPart := uuid.New().String()[:8]
	roomID := "room_0x" + uuidPart
	return roomID
}

// NewRoomManager creates a new room manager instance
func NewRoomManager(maxRooms int) *RoomManager {
	log.Printf("Creating new room manager with max rooms: %d", maxRooms)
	return &RoomManager{
		Rooms:        make(map[string]*game.Room),
		MaxRooms:     maxRooms,
		WaitingRooms: make([]*game.Room, 0),
	}
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

func (rm *RoomManager) FindOrCreateRoom() *game.Room {

	rm.Mutex.Lock()
	defer rm.Mutex.Unlock()

	//Check if any room have slots
	for _, room := range rm.WaitingRooms {
		if len(room.Clients) < constants.MaxmimumPlayers {
			if room.Status == constants.StatusWaiting {
				return room
			}
		}
	}

	//If no slots are found, Create a new one
	roomID := generateRoomID()
	room := game.NewRoom(roomID)
	rm.Rooms[roomID] = room
	rm.WaitingRooms = append(rm.WaitingRooms, room)
	rm.ActiveRooms++

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
