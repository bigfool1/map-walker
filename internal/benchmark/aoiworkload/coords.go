package aoiworkload

import (
	"math"

	"map-walker/internal/game"
)

const metersPerDegreeLatitude = 111_320.0

func latLngToLocal(config game.AOIConfig, lat, lng float64) (localX, localY float64) {
	localY = (lat - config.OriginLat) * metersPerDegreeLatitude
	localX = (lng - config.OriginLng) * metersPerDegreeLongitude(config.OriginLat)
	return localX, localY
}

func metersPerDegreeLongitude(latitude float64) float64 {
	return metersPerDegreeLatitude * math.Cos(latitude*math.Pi/180)
}

func localToCell(config game.AOIConfig, localX, localY float64) game.CellCoord {
	return game.CellCoord{
		X: int(math.Floor(localX / config.CellSizeMeters)),
		Y: int(math.Floor(localY / config.CellSizeMeters)),
	}
}
