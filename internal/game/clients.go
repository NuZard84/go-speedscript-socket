package game

import (
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// PlayerStats tracks individual player performance during the game
type PlayerStats struct {
	IsReady         bool       `json:"isReady"`
	CurrentPosition int        `json:"currentPosition"`
	WPM             float64    `json:"wpm"`
	FinishTime      *time.Time `json:"finishTime,omitempty"`
	Rank            int        `json:"rank"`
}

// Client represents a connected player with their connection and game stats
type Client struct {
	Conn     *websocket.Conn
	Username string
	Room     *Room
	Stats    *PlayerStats
	Mu       sync.RWMutex
	WriteMu  sync.Mutex
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
