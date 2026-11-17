package client

import (
	"testing"

	"github.com/kittors/deneb/proto"
)

// A LAN's picture as the view takes it: none, a preset this client has,
// the owner's own; a preset it does not know, from a newer server, is none.
func TestLANIconFromView(t *testing.T) {
	if !lanIcon(nil).None() {
		t.Error("no picture is one")
	}
	if got := lanIcon(&proto.LANIcon{Preset: "fluent3d/cat"}); got.Preset != "fluent3d/cat" {
		t.Errorf("a preset: %+v", got)
	}
	if got := lanIcon(&proto.LANIcon{Preset: "fluent3d/dragon"}); !got.None() {
		t.Errorf("an unknown preset: %+v", got)
	}
	if got := lanIcon(&proto.LANIcon{Image: []byte{1, 2, 3}}); len(got.Image) != 3 {
		t.Errorf("one's own: %+v", got)
	}
}
