package control

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kittors/deneb/proto"
)

func sameIcon(a, b *proto.LANIcon) bool {
	return a != nil && b != nil && a.Preset == b.Preset && bytes.Equal(a.Image, b.Image)
}

func lanViewOf(e *engine, id proto.LANID) *proto.LANView {
	for _, lv := range e.view().LANs {
		if lv.ID == id {
			return &lv
		}
	}
	return nil
}

// testPicture is a PNG of w×h, as someone's photo would be.
func testPicture(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestLANIcon: a new LAN has one of the presets; its owner changes it, to
// another preset or to a picture of their own, made 128×128, and no one
// else can; what will not do is refused, with the words the dialog shows.
// Every member sees it, one waiting to be let in too, and so does an
// invite's preview; the features say the server has it.
func TestLANIcon(t *testing.T) {
	w := newM2World(t)
	lv := lanViewOf(w.mac, w.studio)
	if lv == nil || lv.Icon == nil || !proto.ValidLANIconPreset(lv.Icon.Preset) || len(lv.Icon.Image) != 0 {
		t.Fatalf("a new LAN's picture: %+v", lv)
	}
	if !slices.Contains(w.mac.view().Features, proto.FeatureLANIcon) {
		t.Fatal("the features do not say lanIcon")
	}

	// Another preset; the same again changes nothing.
	other := "fluent3d/rocket"
	if lv.Icon.Preset == other {
		other = "fluent3d/cat"
	}
	set := proto.OpArgs{LAN: w.studio, Icon: &proto.LANIcon{Preset: other}}
	w.mac.must(proto.OpLANSetIcon, set)
	if got := lanViewOf(w.mac, w.studio).Icon; got == nil || got.Preset != other {
		t.Fatalf("after setting %s: %+v", other, got)
	}
	v := w.s.Version()
	if w.mac.must(proto.OpLANSetIcon, set); w.s.Version() != v {
		t.Fatalf("the same preset again made version %d after %d", w.s.Version(), v)
	}

	// The owner's alone: the Mac is in 周末联机, which is 阿杰's.
	if e := w.mac.refused(proto.ErrForbidden, proto.OpLANSetIcon, proto.OpArgs{LAN: w.weekend, Icon: &proto.LANIcon{Preset: other}}); e.Message != "只有拥有者能换头像" {
		t.Fatalf("a member: %q", e.Message)
	}
	// What will not do: no preset of the list, nothing, both at once.
	big := testPicture(t, 300, 200)
	for _, ic := range []*proto.LANIcon{{Preset: "fluent3d/dragon"}, {}, {Preset: other, Image: big}, nil} {
		w.mac.refused(proto.ErrInvalid, proto.OpLANSetIcon, proto.OpArgs{LAN: w.studio, Icon: ic})
	}
	// A picture it does not take: NormalizeLANIcon's words, as they are.
	// (One over 8 MB the client never sends: it makes the picture 128×128
	// first, and the request may hold a megabyte.)
	notPicture := []byte("GIF89a, not a picture it takes")
	_, want := proto.NormalizeLANIcon(notPicture)
	if e := w.mac.refused(proto.ErrInvalid, proto.OpLANSetIcon, proto.OpArgs{LAN: w.studio, Icon: &proto.LANIcon{Image: notPicture}}); want == nil || e.Message != want.Error() {
		t.Fatalf("a picture it does not take: %q, want %v", e.Message, want)
	}

	// A picture of the owner's: square, 128×128, a PNG.
	w.mac.must(proto.OpLANSetIcon, proto.OpArgs{LAN: w.studio, Icon: &proto.LANIcon{Image: big}})
	got := lanViewOf(w.mac, w.studio).Icon
	if got == nil || got.Preset != "" {
		t.Fatalf("after the picture: %+v", got)
	}
	if cfg, format, err := image.DecodeConfig(bytes.NewReader(got.Image)); err != nil || format != "png" || cfg.Width != 128 || cfg.Height != 128 {
		t.Fatalf("the picture kept: %s %dx%d, %v", format, cfg.Width, cfg.Height, err)
	}
	// Back to a preset.
	w.mac.must(proto.OpLANSetIcon, set)
	if got := lanViewOf(w.mac, w.studio).Icon; got == nil || got.Preset != other || len(got.Image) != 0 {
		t.Fatalf("back to a preset: %+v", got)
	}

	// The members see it; so does one waiting for the owner, invited by
	// a member; so does the invite's preview, logged out.
	weekend := lanViewOf(w.jdesk, w.weekend).Icon
	if seen := lanViewOf(w.mac, w.weekend).Icon; !sameIcon(seen, weekend) {
		t.Fatalf("a member sees %+v, the owner %+v", seen, weekend)
	}
	inv := w.mac.must(proto.OpLANInvite, proto.OpArgs{LAN: w.weekend}).Result.Invite
	mia := newEngine(t, w.mac.url, "mia-mac", proto.MacOS)
	mia.login("mia@deneb.test")
	mia.must(proto.OpInviteAccept, proto.OpArgs{Invite: inv.URL})
	if lv := lanViewOf(mia, w.weekend); lv == nil || !lv.Pending || !sameIcon(lv.Icon, weekend) {
		t.Fatalf("waiting to be let in: %+v", lv)
	}
	token := inv.URL[strings.LastIndex(inv.URL, proto.InvitePath)+len(proto.InvitePath):]
	resp, err := http.Get(w.mac.url + proto.PathWebInvites + token)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var p proto.InvitePreview
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil || !sameIcon(p.Icon, weekend) {
		t.Fatalf("the invite's preview: %+v, %v", p, err)
	}
}

// TestLANIconMigration: migration 14 gives the LANs made before one of
// the first 16 presets, by their names, which never change.
func TestLANIconMigration(t *testing.T) {
	names := regexp.MustCompile(`'(fluent3d/[a-z]+)'`).FindAllStringSubmatch(migrations[13], -1)
	var got []string
	for _, m := range names {
		got = append(got, m[1])
	}
	if !slices.Equal(got, proto.LANIconPresets[:16]) {
		t.Fatalf("migration 14 gives %v", got)
	}
}
