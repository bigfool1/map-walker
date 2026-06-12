package game

const (
	DefaultAppearanceColor = "#3388ff"
	DefaultAppearanceShape = "circle"

	ShapeCircle   = "circle"
	ShapeSquare   = "square"
	ShapeDiamond  = "diamond"
	ShapeTriangle = "triangle"
)

type Appearance struct {
	Color string `json:"color"`
	Shape string `json:"shape"`
}

func DefaultAppearance() Appearance {
	return Appearance{
		Color: DefaultAppearanceColor,
		Shape: DefaultAppearanceShape,
	}
}
