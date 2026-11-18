package main

import (
	"bytes"
	"context"
	"embed"
	"hash/fnv"
	"path"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/proto"
)

// A LAN's picture (design/plan/m7/switch-motion.md §5): one of the presets,
// cute pictures from Fluent Emoji's 3D set (MIT, THIRD_PARTY_NOTICES), or
// the owner's own, cut square on this computer as a device's picture is.
// It shows where the sidebar showed the LAN's first members' balls, beside
// its name over the canvas, in its card and in its region of the "全部"
// view. Without the server's lanIcon, or a picture, the balls stay.

//go:embed lan-icons/*.png
var lanIconFiles embed.FS

// The sizes a LAN's picture shows at: in the sidebar's row, beside the
// title, in a region's name of the "全部" view, in its card, and in the
// dialog, its preview and its choices.
const (
	lanIconRow     = 18 // as wide as the other rows' marks: the names in one line
	lanIconTitle   = 28
	lanIconRegion  = 18
	lanIconCard    = 40
	lanIconPreview = 64
	lanIconChoice  = 36
	lanIconCell    = 48 // a choice's button
	// lanIconRoom is from the title's picture to its words; the line under
	// the title keeps it too, its dot on the picture's middle line.
	lanIconRoom = 12
)

// lanPresets are the presets' pictures, decoded once, by name.
var lanPresets struct {
	once sync.Once
	pics map[string]ui.ImageSource
}

// lanPreset is the picture of the preset named name, or nil.
func lanPreset(name string) ui.ImageSource {
	lanPresets.once.Do(func() {
		lanPresets.pics = map[string]ui.ImageSource{}
		for _, n := range proto.LANIconPresets {
			b, err := lanIconFiles.ReadFile("lan-icons/" + path.Base(n) + ".png")
			if err != nil {
				continue
			}
			if pic, err := ui.DecodeImage(b); err == nil {
				lanPresets.pics[n] = pic
			}
		}
	})
	return lanPresets.pics[name]
}

// lanPicture is a LAN's picture as it shows: own, the owner's, cut into a
// rounded square; else a preset, whole.
type lanPicture struct {
	img ui.ImageSource
	own bool
}

// lanPicOf is the picture icon names: a preset's, or the owner's own,
// decoded once for as long as the LAN keeps it.
func (a *app) lanPicOf(icon model.LANIcon) (lanPicture, bool) {
	if icon.Preset != "" {
		pic := lanPreset(icon.Preset)
		return lanPicture{img: pic}, pic != nil
	}
	if len(icon.Image) == 0 {
		return lanPicture{}, false
	}
	h := fnv.New64a()
	h.Write(icon.Image)
	key := h.Sum64()
	if a.lanImages == nil {
		a.lanImages = map[uint64]ui.ImageSource{}
	}
	pic, ok := a.lanImages[key]
	if !ok {
		var err error
		if pic, err = ui.DecodeImage(icon.Image); err != nil {
			pic = nil
		}
		a.lanImages[key] = pic
	}
	return lanPicture{img: pic, own: true}, pic != nil
}

// lanPic is the picture of the LAN of id, if the server gives LANs
// pictures and it has one.
func (a *app) lanPic(id model.LANID) (lanPicture, bool) {
	l := a.snap.LAN(id)
	if l == nil || !a.snap.Can(model.FeatureLANIcon) {
		return lanPicture{}, false
	}
	return a.lanPicOf(l.Icon)
}

// lanIconView builds a LAN's picture size big: a preset whole, the owner's
// own in a rounded square, its corners a radius of the scale (DESIGN §3.9).
func lanIconView(c *ui.Context, pic lanPicture, size float32) ui.Element {
	if !pic.own {
		return ui.Image(c, pic.img).Size(size, size).Fit(ui.Contain).Shrink(0)
	}
	radius := float32(8)
	switch {
	case size < 20:
		radius = 4
	case size >= lanIconPreview:
		radius = 16
	case size >= lanIconCard:
		radius = 12
	}
	box := ui.Box(c).Size(size, size).Radius(radius).Clip().Shrink(0)
	box.Children(func() { ui.Image(c, pic.img).Size(size, size).Fit(ui.Cover) })
	return box
}

// lanIconEditor is the dialog "局域网头像": the LAN's picture as it would
// be, from the presets or one's own; busy while the picture is read,
// saving while the server takes it.
type lanIconEditor struct {
	lan          model.LANID
	choice       model.LANIcon
	busy, saving bool
	work         busy // the save's, for 保存's ring (dialogButton)
	err          string
	// errWords is err as it shows beside the buttons, coming and going
	// one at a time (§6.5).
	errWords swapper
}

// openLANIcon opens the dialog of the LAN's picture, for its owner.
func (a *app) openLANIcon(id model.LANID) {
	l := a.snap.LAN(id)
	if l == nil || l.Owner != a.snap.Me || !a.snap.Can(model.FeatureLANIcon) {
		return
	}
	a.lanIconEdit = &lanIconEditor{lan: id, choice: l.Icon}
}

// pickLANIcon reads a picture of one's own for the LAN, cut square and
// small on this computer, as a device's (proto.NormalizeLANIcon).
func (a *app) pickLANIcon(e *lanIconEditor) {
	if a.pickAvatar == nil || e.busy || e.saving {
		return
	}
	e.busy, e.err = true, ""
	go func() {
		b, err := a.pickAvatar()
		if err == nil && len(b) > 0 {
			b, err = proto.NormalizeLANIcon(b)
		}
		a.post(func(*ui.Context) {
			e.busy = false
			if a.lanIconEdit != e {
				return
			}
			switch {
			case err != nil:
				e.err = err.Error()
			case len(b) > 0:
				e.choice = model.LANIcon{Image: b}
			}
		})
	}()
}

// same reports whether two pictures are the same.
func sameLANIcon(x, y model.LANIcon) bool {
	return x.Preset == y.Preset && bytes.Equal(x.Image, y.Image)
}

// lanIconLayer is the dialog of a LAN's picture: the picture as it would
// be, its name, "上传图片"; the presets, two rows; "取消", "保存".
func (a *app) lanIconLayer(c *ui.Context, p *palette) {
	e := a.lanIconEdit
	close := func() {
		if e != nil && !e.saving && !e.busy {
			a.lanIconEdit = nil
		}
	}
	modalPanel(c, p, "lan-icon-editor", "局域网头像", 500, e != nil, close, func() {
		l := a.snap.LAN(e.lan)
		if l == nil {
			ui.Text(c, "这个局域网已经不在了").TextColor(p.ink3)
			return
		}
		// Reading a picture of one's own, the rest waits for it, greyed.
		// Saving, nothing greys (DESIGN §5: waiting is not "can't"): 保存
		// says it is under way, and the rest takes no click till it is done.
		reading, quiet := e.busy, e.busy || e.saving
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			if pic, ok := a.lanPicOf(e.choice); ok {
				lanIconView(c, pic, lanIconPreview)
			} else {
				ui.Box(c).Size(lanIconPreview, lanIconPreview).Radius(16).Background(p.well)
			}
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, l.Name).FontSize(15).FontWeight(600).SingleLine().MaxWidth(300)
				label := "上传图片"
				if e.busy {
					label = "读取中…"
				}
				if dialogButton(c, p, "upload", label, false, false, reading).Clicked() {
					a.pickLANIcon(e)
				}
				ui.Text(c, "PNG / JPEG · 最大 8 MB").FontSize(12).TextColor(p.ink3)
			})
		})
		ui.Column(c).Gap(8).Children(func() {
			for row := 0; row*8 < len(proto.LANIconPresets); row++ {
				ui.Row(c).Gap(8).Children(func() {
					for i := row * 8; i < min(row*8+8, len(proto.LANIconPresets)); i++ {
						name := proto.LANIconPresets[i]
						b := ui.ButtonBase(c.Key("lan-icon-"+name)).Size(lanIconCell, lanIconCell).Radius(12).Center().
							Label(lanIconNames[path.Base(name)]).FocusRing(false).Disabled(reading)
						switch {
						case e.choice.Preset == name:
							b.Background(p.primary.Alpha(.1)).Border(1.5, p.primary)
						case b.Hovered() && !quiet:
							b.Background(p.well)
						}
						b.Transition(ui.ElementTransition{Duration: fadeTime, Colors: true})
						b.AnimatePose(pressPose(b, pressGive), ui.Snappy)
						b.Children(func() {
							if pic := lanPreset(name); pic != nil {
								ui.Image(c, pic).Size(lanIconChoice, lanIconChoice).Fit(ui.Contain)
							}
						})
						if b.Clicked() && !quiet {
							e.choice, e.err = model.LANIcon{Preset: name}, ""
						}
					}
				})
			}
		})
		// What went wrong shows beside the buttons, where the eye is once
		// 保存 is pressed, and fades in and out there: the dialog keeps its
		// size as it comes and goes.
		words, alpha, moving := e.errWords.step(e.err, c.Now())
		if moving {
			c.AnimationFrame()
		}
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Grow(1).Shrink(1).Children(func() {
				if words != "" {
					ui.Text(c, words).FontSize(12).TextColor(danger).LineHeight(1.4).MaxLines(2).Opacity(alpha)
				}
			})
			dialogActions(c, func() {
				if dialogButton(c, p, "cancel", "取消", false, false, reading).Clicked() {
					close()
				}
				if dialogButton(c, p, "save", "保存", true, false, quiet || e.choice.None() || sameLANIcon(e.choice, l.Icon), &e.work).Clicked() && !e.work.working(c.Now()) {
					e.saving, e.err = true, ""
					e.work.begin(c.Now())
					op := model.Op{Kind: model.OpLANSetIcon, LAN: e.lan, Icon: e.choice}
					run(a, c, func(ctx context.Context) (model.Result, error) { return a.store.Do(ctx, op) },
						func(c *ui.Context, _ model.Result, err error) {
							e.saving = false
							e.work.end()
							if a.lanIconEdit != e {
								return
							}
							if err != nil {
								e.err = message(err)
								return
							}
							a.lanIconEdit = nil
						})
				}
			})
		})
	})
}

// lanIconNames are what each preset shows, for the screen readers and the
// tests: the label of its button.
var lanIconNames = map[string]string{
	"cat": "小猫", "dog": "小狗", "fox": "狐狸", "panda": "熊猫", "rabbit": "兔子", "frog": "青蛙", "penguin": "企鹅", "whale": "鲸鱼",
	"mushroom": "蘑菇", "cactus": "仙人掌", "rainbow": "彩虹", "planet": "土星", "gamepad": "游戏手柄", "house": "房子", "sunflower": "向日葵", "rocket": "火箭",
}

// lanMark is a LAN's mark in its card: its picture, or its first members'
// balls on a well; for the owner, a button that opens "局域网头像".
func (a *app) lanMark(c *ui.Context, p *palette, l *model.LAN) {
	pic, ok := a.lanPic(l.ID)
	mine := l.Owner == a.snap.Me && a.snap.Can(model.FeatureLANIcon)
	mark := ui.ButtonBase(c.Key("lan-mark")).Size(lanIconCard, lanIconCard).Radius(12).Center().FocusRing(false).Shrink(0)
	if !ok {
		mark.Background(p.well)
	}
	if mine {
		mark.Label("换头像").Cursor(ui.CursorPointer)
		mark.AnimatePose(pressPose(mark, pressGive), ui.Snappy)
		if mark.Hovered() {
			mark.Shadow(0, 0, 0, 2, p.primary.Alpha(0.35))
		}
		mark.Tooltip("换头像")
	} else {
		mark.PassThrough()
	}
	mark.Children(func() {
		if ok {
			lanIconView(c, pic, lanIconCard)
			return
		}
		ui.Row(c).Children(func() {
			for i, id := range l.Members[:min(3, len(l.Members))] {
				b := ball(c, a.people[string(id)], 16, false).Shadow(0, 0, 0, 1.5, p.well)
				if i > 0 {
					b.Margin(0, 0, 0, -4)
				}
			}
		})
	})
	if mine && mark.Clicked() {
		a.openLANIcon(l.ID)
	}
}
