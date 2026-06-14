package synthetic

import "testing"

func TestFormatUsername(t *testing.T) {
	if got := FormatUsername(1); got != "synthetic_1" {
		t.Fatalf("FormatUsername(1) = %q, want synthetic_1", got)
	}
	if got := FormatUsername(42); got != "synthetic_42" {
		t.Fatalf("FormatUsername(42) = %q, want synthetic_42", got)
	}
}

func TestHasReservedPrefix(t *testing.T) {
	reserved := []string{
		"synthetic_1",
		"Synthetic_1",
		"SYNTHETIC_abc",
		"synthetic_",
	}
	for _, username := range reserved {
		if !HasReservedPrefix(username) {
			t.Fatalf("expected reserved prefix for %q", username)
		}
	}

	allowed := []string{
		"alice",
		"synthetic",
		"notsynthetic_1",
		"my_synthetic_1",
		"syntheticlookalike",
	}
	for _, username := range allowed {
		if HasReservedPrefix(username) {
			t.Fatalf("expected no reserved prefix for %q", username)
		}
	}
}

func TestParseUsernameAcceptsCanonicalNames(t *testing.T) {
	cases := map[string]int{
		"synthetic_1":   1,
		"Synthetic_42":  42,
		"SYNTHETIC_999": 999,
	}
	for username, want := range cases {
		got, ok := ParseUsername(username)
		if !ok || got != want {
			t.Fatalf("ParseUsername(%q) = (%d, %v), want (%d, true)", username, got, ok, want)
		}
	}
}

func TestParseUsernameRejectsInvalidSuffixes(t *testing.T) {
	rejected := []string{
		"synthetic_",
		"synthetic_0",
		"synthetic_-1",
		"synthetic_01",
		"synthetic_1a",
		"synthetic_abc",
		"alice",
		"synthetic",
		"notsynthetic_1",
	}
	for _, username := range rejected {
		if _, ok := ParseUsername(username); ok {
			t.Fatalf("ParseUsername(%q) expected false", username)
		}
	}
}
