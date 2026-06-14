package auth

import (
	"errors"
	"path/filepath"
	"testing"

	"map-walker/internal/storage"
)

func TestValidateAppearanceAcceptsNormalizedColor(t *testing.T) {
	appearance, err := ValidateAppearance("#FF6600", "diamond")
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if appearance.Color != "#ff6600" || appearance.Shape != "diamond" {
		t.Fatalf("unexpected appearance: %+v", appearance)
	}
}

func TestValidateAppearanceRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		color string
		shape string
	}{
		{"", "circle"},
		{"#ff6600", ""},
		{"ff6600", "circle"},
		{"#ff66", "circle"},
		{"#gg6600", "circle"},
		{"#ff6600", "hexagon"},
	}
	for _, tc := range cases {
		if _, err := ValidateAppearance(tc.color, tc.shape); !errors.Is(err, ErrInvalidAppearance) {
			t.Fatalf("expected invalid appearance for %+v, got %v", tc, err)
		}
	}
}

func TestSaveAppearanceReturnsStorageError(t *testing.T) {
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc := NewService(db)
	err = svc.SaveAppearance(0, storage.Appearance{
		Color: "#ff6600",
		Shape: "diamond",
	})
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}
