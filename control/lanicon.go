package control

import (
	"bytes"
	"math/rand/v2"
	"slices"

	"github.com/kittors/deneb/proto"
)

// A LAN's picture (design/plan/m7/switch-motion.md §5.4): one of the
// presets every client carries, by its name, or its owner's own, a 128×128
// PNG (proto.NormalizeLANIcon). It is in the LAN's row, so it goes when
// the LAN does. A new LAN gets a preset at random; the LANs made before had
// one given by migration 14; either stays until the owner changes it.
//
// Every member sees it, those waiting to be let in too, and anyone holding
// an invite to the LAN (InvitePreview). It goes inline, as a device's
// avatar does: a preset is a name, a picture a PNG of 128×128; the
// engine's network map never carries it.

// randomLANIcon is a new LAN's preset.
func randomLANIcon() string {
	return proto.LANIconPresets[rand.IntN(len(proto.LANIconPresets))]
}

// icon is l's picture as the views carry it; nil for a LAN with none.
func (l *lan) icon() *proto.LANIcon {
	switch {
	case len(l.IconImage) > 0:
		return &proto.LANIcon{Image: slices.Clone(l.IconImage)}
	case l.IconPreset != "":
		return &proto.LANIcon{Preset: l.IconPreset}
	}
	return nil
}

// opLANSetIcon sets a LAN's picture: a preset by its name, or a picture,
// which proto.NormalizeLANIcon makes 128×128; when it will not do, its
// words go back as they are, for the dialog shows them. The owner's only.
func opLANSetIcon(c *opCtx, a proto.OpArgs, out *proto.OpOutput) error {
	l, err := c.lan(a.LAN)
	if err != nil {
		return err
	}
	if err := c.ownerOnly(l, "只有拥有者能换头像"); err != nil {
		return err
	}
	ic := a.Icon
	switch {
	case ic == nil || ic.Preset == "" && len(ic.Image) == 0:
		return errInvalid("要给一个头像：选一个，或者上传一张图")
	case ic.Preset != "" && len(ic.Image) > 0:
		return errInvalid("头像要么选一个，要么上传一张图，不能两样都给")
	case ic.Preset != "":
		if !proto.ValidLANIconPreset(ic.Preset) {
			return errInvalid("没有这个头像，请重新选一个")
		}
		if ic.Preset == l.IconPreset && len(l.IconImage) == 0 {
			return errNoChange
		}
		return c.exec(`UPDATE lans SET icon_preset = ?, icon_image = X'' WHERE id = ?`, ic.Preset, l.ID)
	}
	img, err := proto.NormalizeLANIcon(ic.Image)
	if err != nil {
		return errInvalid(err.Error())
	}
	if bytes.Equal(img, l.IconImage) {
		return errNoChange
	}
	return c.exec(`UPDATE lans SET icon_preset = '', icon_image = ? WHERE id = ?`, img, l.ID)
}
