package proto

import (
	"strings"
	"testing"
)

// The presets' names are the protocol's: sixteen, each once, each a
// Fluent 3D picture's; nothing else passes for one.
func TestLANIconPresets(t *testing.T) {
	if len(LANIconPresets) != 16 {
		t.Errorf("%d presets, want 16", len(LANIconPresets))
	}
	seen := map[string]bool{}
	for _, n := range LANIconPresets {
		if !strings.HasPrefix(n, "fluent3d/") || seen[n] || !ValidLANIconPreset(n) {
			t.Errorf("preset %q", n)
		}
		seen[n] = true
	}
	for _, n := range []string{"", "fluent3d/", "fluent3d/dragon", "cat", "FLUENT3D/CAT"} {
		if ValidLANIconPreset(n) {
			t.Errorf("%q passes for a preset", n)
		}
	}
}
