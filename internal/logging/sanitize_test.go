package logging

import (
	"strings"
	"testing"
)

func TestTruncateCutsLongValues(t *testing.T) {
	truncated := Truncate(strings.Repeat("x", 500), 10)

	if len([]rune(truncated)) != 11 {
		t.Fatalf("Truncate() returned %d runes, want 10 plus the ellipsis", len([]rune(truncated)))
	}
	if !strings.HasSuffix(truncated, "…") {
		t.Fatalf("Truncate() = %q, want the ellipsis marker", truncated)
	}
}

func TestTruncateKeepsShortValues(t *testing.T) {
	if got := Truncate("short", 10); got != "short" {
		t.Fatalf("Truncate() = %q, want %q", got, "short")
	}
}

func TestTruncateWithoutLimitKeepsTheValue(t *testing.T) {
	long := strings.Repeat("x", 500)

	if got := Truncate(long, 0); got != long {
		t.Fatalf("Truncate() with no limit returned %d runes, want %d", len([]rune(got)), len([]rune(long)))
	}
}
