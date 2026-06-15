package realtime

import "map-walker/internal/game"

type SavedPlayerLoad struct {
	Username    string
	Lat         float64
	Lng         float64
	HasPosition bool
	Appearance  game.Appearance
	Score       int64
	IsSynthetic bool
}

type SavedPlayerLoader func(userID int64) (SavedPlayerLoad, bool)
