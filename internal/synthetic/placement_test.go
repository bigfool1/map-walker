package synthetic

import (
	"testing"
)

func TestPlacementLatLngIsDeterministic(t *testing.T) {
	cfg := DefaultPlacementConfig()

	firstLat, firstLng := PlacementLatLng(cfg, 1)
	secondLat, secondLng := PlacementLatLng(cfg, 1)
	if firstLat != secondLat || firstLng != secondLng {
		t.Fatalf("expected stable placement, got (%v,%v) and (%v,%v)", firstLat, firstLng, secondLat, secondLng)
	}
}

func TestPlacementLatLngStaysWithinActivityRegion(t *testing.T) {
	for _, accountNumber := range []int{1, 42, 9999} {
		if !WithinActivityRegion(accountNumber) {
			t.Fatalf("account %d placement outside activity region", accountNumber)
		}
	}
}

func TestPlacementLatLngIsSpatiallyDistributed(t *testing.T) {
	distance := LocalPlacementDistance(1, 9999)
	if distance < 100 {
		t.Fatalf("expected low and high accounts to be separated, got %vm", distance)
	}
}

func TestFixedAppearance(t *testing.T) {
	appearance := FixedAppearance()
	if appearance.Color != AppearanceColor || appearance.Shape != AppearanceShape {
		t.Fatalf("unexpected fixed appearance: %+v", appearance)
	}
}
