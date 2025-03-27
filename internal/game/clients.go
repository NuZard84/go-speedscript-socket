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
	HighestWpm      struct {
		HighestScore10s  float64 `bson:"highestScore10s" json:"highestScore10s"`
		HighestScore30s  float64 `bson:"highestScore30s" json:"highestScore30s"`
		HighestScore60s  float64 `bson:"highestScore60s" json:"highestScore60s"`
		HighestScore120s float64 `bson:"highestScore120s" json:"highestScore120s"`
	} `bson:"highestWpm" json:"highestWpm"`
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
	HighestWpm struct {
		HighestScore10s  float64 `bson:"highestScore10s" json:"highestScore10s"`
		HighestScore30s  float64 `bson:"highestScore30s" json:"highestScore30s"`
		HighestScore60s  float64 `bson:"highestScore60s" json:"highestScore60s"`
		HighestScore120s float64 `bson:"highestScore120s" json:"highestScore120s"`
	} `bson:"highestWpm" json:"highestWpm"`
}

func setProfileFromDb(username string) *UserProfile {
	ctx := context.Background()
	userProfile, err := db.GetUserProfile(ctx, username)

	if err != nil {
		log.Printf("Error fetching User profile for %s: %v", username, err)
		return &UserProfile{
			HighestWpm: struct {
				HighestScore10s  float64 `bson:"highestScore10s" json:"highestScore10s"`
				HighestScore30s  float64 `bson:"highestScore30s" json:"highestScore30s"`
				HighestScore60s  float64 `bson:"highestScore60s" json:"highestScore60s"`
				HighestScore120s float64 `bson:"highestScore120s" json:"highestScore120s"`
			}{
				HighestScore10s:  0,
				HighestScore30s:  0,
				HighestScore60s:  0,
				HighestScore120s: 0,
			},
		}
	}

	if userProfile == nil {
		log.Printf("No profile found for user %s", username)
		return &UserProfile{
			HighestWpm: struct {
				HighestScore10s  float64 `bson:"highestScore10s" json:"highestScore10s"`
				HighestScore30s  float64 `bson:"highestScore30s" json:"highestScore30s"`
				HighestScore60s  float64 `bson:"highestScore60s" json:"highestScore60s"`
				HighestScore120s float64 `bson:"highestScore120s" json:"highestScore120s"`
			}{
				HighestScore10s:  0,
				HighestScore30s:  0,
				HighestScore60s:  0,
				HighestScore120s: 0,
			},
		}
	}

	return &UserProfile{
		HighestWpm: struct {
			HighestScore10s  float64 `bson:"highestScore10s" json:"highestScore10s"`
			HighestScore30s  float64 `bson:"highestScore30s" json:"highestScore30s"`
			HighestScore60s  float64 `bson:"highestScore60s" json:"highestScore60s"`
			HighestScore120s float64 `bson:"highestScore120s" json:"highestScore120s"`
		}{
			HighestScore10s:  userProfile.HighestWpm.HighestScore10s,
			HighestScore30s:  userProfile.HighestWpm.HighestScore30s,
			HighestScore60s:  userProfile.HighestWpm.HighestScore60s,
			HighestScore120s: userProfile.HighestWpm.HighestScore120s,
		},
	}
}

// NewClient creates a new client instance with initialized stats
func NewClient(conn *websocket.Conn, username string) *Client {
	log.Printf("New client connected: %s", username)

	if username == "player" {
		return &Client{
			Conn:     conn,
			Username: username,
		}
	} else {
		profile := setProfileFromDb(username)
		return &Client{
			Conn:     conn,
			Username: username,
			Stats: &PlayerStats{
				CurrentPosition: 0,
				WPM:             0,
				HighestWpm: struct {
					HighestScore10s  float64 `bson:"highestScore10s" json:"highestScore10s"`
					HighestScore30s  float64 `bson:"highestScore30s" json:"highestScore30s"`
					HighestScore60s  float64 `bson:"highestScore60s" json:"highestScore60s"`
					HighestScore120s float64 `bson:"highestScore120s" json:"highestScore120s"`
				}{
					HighestScore10s:  profile.HighestWpm.HighestScore10s,
					HighestScore30s:  profile.HighestWpm.HighestScore30s,
					HighestScore60s:  profile.HighestWpm.HighestScore60s,
					HighestScore120s: profile.HighestWpm.HighestScore120s,
				},
			},
			UserProfile: *profile,
		}
	}
}

// GetHighestWpmForDuration returns the highest WPM for a specific duration
func (c *Client) GetHighestWpmForDuration(duration int) float64 {
	c.Mu.RLock()
	defer c.Mu.RUnlock()

	switch duration {
	case 10:
		return c.UserProfile.HighestWpm.HighestScore10s
	case 30:
		return c.UserProfile.HighestWpm.HighestScore30s
	case 60:
		return c.UserProfile.HighestWpm.HighestScore60s
	case 120:
		return c.UserProfile.HighestWpm.HighestScore120s
	default:
		return 0
	}
}
