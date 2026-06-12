package realtime

import "map-walker/internal/game"

type SavedPlayerLoad struct {
	Lat         float64
	Lng         float64
	HasPosition bool
	Appearance  game.Appearance
}

type SavedPlayerLoader func(userID string) (SavedPlayerLoad, bool)
