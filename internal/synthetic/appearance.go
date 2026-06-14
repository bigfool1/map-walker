package synthetic

import "map-walker/internal/storage"

const (
	AppearanceColor = "#ff8c00"
	AppearanceShape = "diamond"
)

func FixedAppearance() storage.Appearance {
	return storage.Appearance{
		Color: AppearanceColor,
		Shape: AppearanceShape,
	}
}
