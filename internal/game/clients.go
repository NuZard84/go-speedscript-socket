package game

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/NuZard84/go-socket-speedscript/internal/db"
	"github.com/gorilla/websocket"
)

// PlayerStats tracks individual player performance during the game
type PlayerStats struct {
	IsReady         bool       `json:"isReady"`
	CurrentPosition int        `json:"currentPosition"`
	WPM             float64    `json:"wpm"`
	FinishTime      *time.Time `json:"finishTime,omitempty"`
	Rank            int        `json:"rank"`
	HighestWpm      float64    `json:"highestWpm"`
}

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
	HighestWpm float64 `bson:"highestWpm"`
}

func setProfileFromDb(username string) *UserProfile {
	ctx := context.Background()
	userProfile, err := db.GetUserProfile(ctx, username)

	if err != nil {
		log.Printf("Error fetching User profile for %s: %v", username, err)
		return &UserProfile{
			HighestWpm: 0,
		}
	}

	if userProfile == nil {
		log.Printf("No profile found for user %s", username)
		return &UserProfile{
			HighestWpm: 0,
		}
	}

	return &UserProfile{
		HighestWpm: userProfile.HighestWpm,
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
		UserProfile: *setProfileFromDb(username),
	}
}
