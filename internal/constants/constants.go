package constants

import "time"

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
