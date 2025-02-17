package manager

import (
	"fmt"
	"log"
	"sync"

	"github.com/NuZard84/go-socket-speedscript/internal/constants"
	"github.com/NuZard84/go-socket-speedscript/internal/game"

	"github.com/google/uuid"
)

// RoomManager handles the creation and management of game rooms
type RoomManager struct {
	Rooms        map[string]*game.Room
	PrivateRooms map[string]*game.Room
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

func generatePrivateRoomID() string {
	uuidPart := uuid.New().String()[:8]
	roomID := "pr_room_0x" + uuidPart
	return roomID
}

// NewRoomManager creates a new room manager instance
func NewRoomManager(maxRooms int) *RoomManager {
	log.Printf("Creating new room manager with max rooms: %d", maxRooms)
	return &RoomManager{
		Rooms:        make(map[string]*game.Room),
		PrivateRooms: make(map[string]*game.Room),
		MaxRooms:     maxRooms,
		WaitingRooms: make([]*game.Room, 0),
	}
}

// RemoveRoom removes a room from the room manager
func (rm *RoomManager) RemoveRoom(roomID string) {
	rm.Mutex.Lock()
	defer rm.Mutex.Unlock()

	//  remove from public rooms.
	if _, ok := rm.Rooms[roomID]; ok {
		delete(rm.Rooms, roomID)
		rm.ActiveRooms--
		rm.removeFromWaitingRooms(roomID)
		log.Printf("Public room removed: %s, Active rooms: %d", roomID, rm.ActiveRooms)
		return
	}

	//  remove from private rooms.
	if _, ok := rm.PrivateRooms[roomID]; ok {
		delete(rm.PrivateRooms, roomID)
		rm.ActiveRooms--
		log.Printf("Private room removed: %s, Active rooms: %d", roomID, rm.ActiveRooms)
		return
	}

	log.Printf("Room %s does not exist!", roomID)
}

func (rm *RoomManager) removeFromWaitingRooms(roomID string) {
	for i, room := range rm.WaitingRooms {
		if room.ID == roomID {
			rm.WaitingRooms = append(rm.WaitingRooms[:i], rm.WaitingRooms[i+1:]...)
			break
		}
	}
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
	room := game.NewRoom(roomID, "", constants.MaxmimumPlayers)
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

func (rm *RoomManager) GetRoom(RoomID string, isCustom bool) (*game.Room, error) {
	rm.Mutex.Lock()
	defer rm.Mutex.Unlock()

	if isCustom {
		room, exist := rm.PrivateRooms[RoomID]
		if !exist {
			return nil, fmt.Errorf("private room %s not found", RoomID)
		}
		return room, nil
	} else {
		room, exist := rm.Rooms[RoomID]
		if !exist {
			return nil, fmt.Errorf("room %s not found", RoomID)
		}
		return room, nil
	}
}

func (rm *RoomManager) CreateCustomRoom(adminUsername string, capcity int) *game.Room {
	rm.Mutex.Lock()
	defer rm.Mutex.Unlock()

	roomID := generatePrivateRoomID()
	room := game.NewRoom(roomID, adminUsername, capcity)
	rm.PrivateRooms[roomID] = room
	rm.ActiveRooms++
	log.Printf("Created custom private room: %s", roomID)

	return room
}
