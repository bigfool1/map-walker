package storage

const (
	DefaultAppearanceColor = "#3388ff"
	DefaultAppearanceShape = "circle"
)

type Appearance struct {
	Color string
	Shape string
}

func appearanceOrDefault(appearance Appearance) Appearance {
	if appearance.Color == "" {
		appearance.Color = DefaultAppearanceColor
	}
	if appearance.Shape == "" {
		appearance.Shape = DefaultAppearanceShape
	}
	return appearance
}
