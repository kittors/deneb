package proto

import "slices"

// LANIcon is a LAN's picture (design/plan/m7/switch-motion.md §5): one of
// the presets every client carries, by its name, or the owner's own
// picture, a square PNG of 128×128 made by NormalizeLANIcon. lan.setIcon
// sets it; every member of the LAN sees it, and anyone holding an invite
// link to the LAN (InvitePreview). A LAN without one shows its members'
// balls, as before.
type LANIcon struct {
	Preset string `json:"preset,omitempty"`
	Image  []byte `json:"image,omitempty"`
}

// LANIconPresets are the presets' names, in the order the clients offer
// them. A new LAN gets one of them at random. They are part of the
// protocol: a name, once given, keeps its picture and never goes, since
// LANs keep it; new ones are added at the end.
var LANIconPresets = []string{
	"fluent3d/cat", "fluent3d/dog", "fluent3d/fox", "fluent3d/panda",
	"fluent3d/rabbit", "fluent3d/frog", "fluent3d/penguin", "fluent3d/whale",
	"fluent3d/mushroom", "fluent3d/cactus", "fluent3d/rainbow", "fluent3d/planet",
	"fluent3d/gamepad", "fluent3d/house", "fluent3d/sunflower", "fluent3d/rocket",
}

// ValidLANIconPreset reports whether name is one of the presets.
func ValidLANIconPreset(name string) bool { return slices.Contains(LANIconPresets, name) }

// NormalizeLANIcon takes a picture for a LAN as NormalizeDeviceAvatar takes
// one for a device: PNG or JPEG up to 8 MB, cut square from its middle,
// 128×128, a PNG without the photo's metadata. The client does it before
// it uploads; the server does it again.
func NormalizeLANIcon(data []byte) ([]byte, error) { return NormalizeDeviceAvatar(data) }
