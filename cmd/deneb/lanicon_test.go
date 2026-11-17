package main

import (
	"bytes"
	"image/png"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/proto"
)

// A LAN's picture (design/plan/m7/switch-motion.md §5).

// Every preset has its picture, and every picture is a preset's: the
// names are the protocol's, and the files are what the client draws.
func TestLANIconPresetsEmbedded(t *testing.T) {
	files, err := lanIconFiles.ReadDir("lan-icons")
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, f := range files {
		have["fluent3d/"+strings.TrimSuffix(f.Name(), ".png")] = true
		b, _ := lanIconFiles.ReadFile("lan-icons/" + f.Name())
		cfg, err := png.DecodeConfig(bytes.NewReader(b))
		if err != nil || cfg.Width != 128 || cfg.Height != 128 {
			t.Errorf("%s: %dx%d, %v; want a 128×128 PNG", f.Name(), cfg.Width, cfg.Height, err)
		}
	}
	for _, n := range proto.LANIconPresets {
		if !have[n] || lanPreset(n) == nil {
			t.Errorf("preset %s has no picture", n)
		}
		if lanIconNames[path.Base(n)] == "" {
			t.Errorf("preset %s has no name for people", n)
		}
		delete(have, n)
	}
	for n := range have {
		t.Errorf("%s is no preset", n)
	}
}

// iconTestRig is the demo, showing 周末联机, with its picture.
func iconTestRig(t *testing.T) *m3rig {
	t.Helper()
	a, d := newDemoApp()
	d.Manual()
	r := &m3rig{t: t, a: a, d: d, tt: ui.NewTester(a.view, 1180, 740)}
	r.run(800 * time.Millisecond)
	return r
}

// The owner opens the LAN's card, its picture, picks 小猫 and saves: the
// LAN has it, the dialog is gone. Saving what it has already is off.
func TestLANIconPickAndSave(t *testing.T) {
	r := iconTestRig(t)
	if _, ok := r.a.lanPic("weekend"); !ok {
		t.Fatal("周末联机 shows no picture")
	}
	r.a.selected = lanCard
	r.run(500 * time.Millisecond)
	r.click("换头像")
	if r.a.lanIconEdit == nil {
		t.Fatal("no dialog after 换头像")
	}
	if b, ok := r.tt.Find("保存"); !ok || b.W == 0 {
		t.Fatalf("no 保存: %q", r.tt.Texts())
	}
	r.click("小猫")
	r.click("保存")
	r.run(500 * time.Millisecond)
	if got := r.d.Snapshot().LAN("weekend").Icon; got.Preset != "fluent3d/cat" {
		t.Errorf("周末联机's picture: %+v", got)
	}
	if r.a.lanIconEdit != nil {
		t.Error("the dialog stays after saving")
	}
}

// A picture of one's own: cut square and small here, saved as it is.
func TestLANIconOwnPicture(t *testing.T) {
	r := iconTestRig(t)
	r.a.pickAvatar = func() ([]byte, error) { return samplePhoto(), nil }
	r.a.openLANIcon("weekend")
	r.run(300 * time.Millisecond)
	r.click("上传图片")
	r.run(300 * time.Millisecond)
	e := r.a.lanIconEdit
	if e == nil || len(e.choice.Image) == 0 {
		t.Fatalf("no picture of one's own: %+v", e)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(e.choice.Image))
	if err != nil || cfg.Width != 128 || cfg.Height != 128 {
		t.Errorf("the picture is %dx%d, %v; want 128×128", cfg.Width, cfg.Height, err)
	}
	r.click("保存")
	r.run(500 * time.Millisecond)
	if !bytes.Equal(r.d.Snapshot().LAN("weekend").Icon.Image, e.choice.Image) {
		t.Error("the LAN does not have the picture saved")
	}
	if pic, ok := r.a.lanPic("weekend"); !ok || !pic.own {
		t.Error("the LAN's own picture does not show as one")
	}
}

// A picture that is not one: the dialog says so, and stays.
func TestLANIconBadPicture(t *testing.T) {
	r := iconTestRig(t)
	r.a.pickAvatar = func() ([]byte, error) { return []byte("not a picture"), nil }
	r.a.openLANIcon("weekend")
	r.run(300 * time.Millisecond)
	r.click("上传图片")
	r.run(300 * time.Millisecond)
	if e := r.a.lanIconEdit; e == nil || e.err == "" || !r.hasPart(e.err) {
		t.Fatalf("no word of the bad picture: %+v, %q", e, r.tt.Texts())
	}
}

// Only the owner changes it: a LAN of someone else's has no 换头像.
func TestLANIconOwnerOnly(t *testing.T) {
	r := iconTestRig(t)
	r.d.Simulate("waiting")
	r.run(300 * time.Millisecond)
	r.a.openLANIcon("books")
	if r.a.lanIconEdit != nil {
		t.Error("the dialog opens on Leo's LAN")
	}
}

// A server without the LANs' pictures: the members' balls, as before.
func TestLANIconWithoutTheFeature(t *testing.T) {
	r := iconTestRig(t)
	r.d.Simulate("no-lan-icons")
	r.run(300 * time.Millisecond)
	if _, ok := r.a.lanPic("weekend"); ok {
		t.Error("a picture shows without the server's lanIcon")
	}
	r.a.selected = lanCard
	r.run(500 * time.Millisecond)
	if r.has("换头像") {
		t.Error("换头像 offered without the server's lanIcon")
	}
	r.a.openLANIcon("weekend")
	if r.a.lanIconEdit != nil {
		t.Error("the dialog opens without the server's lanIcon")
	}
}

// A new LAN made here gets a picture of its own, a preset.
func TestLANIconNewLAN(t *testing.T) {
	r := iconTestRig(t)
	r.a.post(func(c *ui.Context) { r.a.send(c, model.Op{Kind: model.OpLANCreate, Name: "家里"}) })
	r.run(500 * time.Millisecond)
	for _, l := range r.d.Snapshot().LANs {
		if l.Name == "家里" && !proto.ValidLANIconPreset(l.Icon.Preset) {
			t.Errorf("家里's picture: %+v", l.Icon)
		}
	}
}
