package models

import (
	"time"
)

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
