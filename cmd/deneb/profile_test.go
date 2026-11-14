package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/proto"
)

func TestThisComputerCanRenameAndUpload(t *testing.T) {
	for _, dark := range []bool{false, true} {
		d := client.NewDemo()
		d.Manual()
		d.Snapshot().Features["deviceAvatar"] = true
		a := newApp(d, demoPlaces())
		a.selected = string(d.Snapshot().Self)
		var b bytes.Buffer
		png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 8, 8)))
		a.pickAvatar = func() ([]byte, error) { return proto.NormalizeDeviceAvatar(b.Bytes()) }
		tt := ui.NewTester(a.view, 980, 640)
		tt.SetDark(dark)
		tt.Frame()
		tt.Advance(800 * time.Millisecond)
		if !tt.HasText("重命名") || !tt.HasText("修改头像") {
			t.Fatalf("self profile controls missing: %q", tt.Texts())
		}
		tt.Click("重命名")
		tt.Frame()
		tt.Advance(300 * time.Millisecond)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type("我的测试电脑")
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		tt.Advance(300 * time.Millisecond)
		if got := d.Snapshot().Device(d.Snapshot().Self).Name; got != "我的测试电脑" {
			t.Fatalf("self rename = %q", got)
		}
		tt.Click("修改头像")
		tt.Frame()
		tt.Advance(250 * time.Millisecond)
		tt.Click("上传图片")
		tt.Frame()
		for i := 0; i < 100 && a.avatarEdit != nil && a.avatarEdit.picture == nil; i++ {
			time.Sleep(time.Millisecond)
			tt.Frame()
		}
		tt.Click("保存")
		tt.Frame()
		tt.Advance(250 * time.Millisecond)
		if len(d.Snapshot().Device(d.Snapshot().Self).Avatar) == 0 {
			t.Fatal("avatar not published")
		}
		tt.Frame()
		tt.Advance(300 * time.Millisecond)
		if a.avatar(d.Snapshot().Self) == nil {
			t.Fatal("uploaded avatar not rendered")
		}
		a.openAvatar(d.Snapshot().Self)
		tt.Frame()
		tt.Advance(250 * time.Millisecond)
		tt.Click("恢复默认")
		tt.Frame()
		tt.Advance(250 * time.Millisecond)
		tt.Click("确认恢复图标")
		tt.Frame()
		tt.Advance(250 * time.Millisecond)
		tt.Click("保存")
		tt.Frame()
		if len(d.Snapshot().Device(d.Snapshot().Self).Avatar) != 0 {
			t.Fatal("avatar not cleared")
		}

	}
}
func TestDistinctServiceNamesShowPorts(t *testing.T) {
	a := service{name: "量子大屏", svc: model.Service{Key: "tcp/80", Ports: "80"}}
	b := service{name: "量子大屏", svc: model.Service{Key: "tcp/19080", Ports: "19080"}}
	if serviceLabel([]service{a, b}, a) != "量子大屏 · 80" || serviceLabel([]service{a, b}, b) != "量子大屏 · 19080" {
		t.Fatal("same-name services are indistinguishable")
	}
	if serviceLabel([]service{a}, a) != "量子大屏" {
		t.Fatal("single service label changed")
	}
}
func TestAppUpdateStages(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	a := newApp(d, demoPlaces())
	u := newAppUpdater(a)
	a.updates = u
	var installed bool
	u.check = func(context.Context) (*appRelease, error) {
		return &appRelease{version: "0.2.0", install: func(ctx context.Context, progress func(int64, int64)) error {
			progress(3, 10)
			progress(10, 10)
			installed = true
			return nil
		}}, nil
	}
	tt := ui.NewTester(a.view, 980, 640)
	u.beginCheck(true)
	for i := 0; i < 100 && u.stage == updateChecking; i++ {
		time.Sleep(time.Millisecond)
		tt.Frame()
	}
	if u.stage != updateAvailable {
		t.Fatalf("stage=%s", u.stage)
	}
	u.download()
	for i := 0; i < 100 && u.stage != updateReady; i++ {
		time.Sleep(time.Millisecond)
		tt.Frame()
	}
	if !installed || u.stage != updateReady || u.downloaded != 10 {
		t.Fatalf("update failed: %+v", u)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	if !tt.HasText("更新已就绪") || !tt.HasText("重启并更新") || !tt.HasText("暂不") {
		t.Fatalf("ready state missing: %q", tt.Texts())
	}
	var restarted bool
	u.relaunch = func() { restarted = true }
	tt.Click("暂不")
	tt.Frame()
	if restarted || u.open {
		t.Fatal("later restarted")
	}
	u.open = true
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Click("重启并更新")
	tt.Frame()
	if !restarted {
		t.Fatal("restart not requested")
	}
}

func TestAppUpdateFailureCancelAndRetry(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	a := newApp(d, demoPlaces())
	u := newAppUpdater(a)
	a.updates = u
	tt := ui.NewTester(a.view, 980, 640)
	wait := func(stage updateStage) {
		t.Helper()
		for i := 0; i < 200 && u.stage != stage; i++ {
			time.Sleep(time.Millisecond)
			tt.Frame()
		}
		if u.stage != stage {
			t.Fatalf("stage=%s want=%s", u.stage, stage)
		}
	}
	u.check = func(context.Context) (*appRelease, error) { return nil, errors.New("offline") }
	u.beginCheck(true)
	wait(updateFailed)
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	if !tt.HasText("重试") {
		t.Fatal("check failure has no retry")
	}
	u.check = func(context.Context) (*appRelease, error) {
		return &appRelease{version: "0.2.0", install: func(ctx context.Context, _ func(int64, int64)) error { <-ctx.Done(); return ctx.Err() }}, nil
	}
	u.beginCheck(true)
	wait(updateAvailable)
	u.download()
	u.cancel()
	wait(updateAvailable)
	if u.err == "" {
		t.Fatal("cancellation disappeared")
	}
	u.release.install = func(context.Context, func(int64, int64)) error { return errors.New("bad signature") }
	u.download()
	wait(updateFailed)
	if u.stage == updateReady {
		t.Fatal("bad signature was ready to restart")
	}
	u.release.install = func(context.Context, func(int64, int64)) error { return nil }
	u.download()
	wait(updateReady)
}

func TestAccountOpensSelfProfile(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	a := newApp(d, demoPlaces())
	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Click("账号")
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	if err := tt.Click("编辑这台电脑"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	if a.selected != string(a.snap.Self) || !tt.HasText("重命名") {
		t.Fatal("account did not open self profile")
	}
}

func TestSidebarFooterStaysInOneRow(t *testing.T) {
	for _, stage := range []updateStage{updateAvailable, updateDownloading, updateReady} {
		for _, dark := range []bool{false, true} {
			d := client.NewDemo()
			d.Manual()
			a := newApp(d, demoPlaces())
			a.updates = newAppUpdater(a)
			a.updates.stage = stage
			a.updates.release = &appRelease{version: "0.2.0"}
			a.updates.total = 100
			a.updates.downloaded = 35
			// The modal expands the footer entry too, as in the user's reference.
			a.updates.open = stage == updateReady
			tt := ui.NewTester(a.view, 980, 640)
			tt.SetDark(dark)
			tt.Frame()
			tt.Advance(400 * time.Millisecond)
			account, ok := tt.Find("账号")
			if !ok {
				t.Fatal("account missing")
			}
			update, ok := tt.Find(a.updates.title())
			if !ok {
				t.Fatal("update missing")
			}
			delta := (account.Y + account.H/2) - (update.Y + update.H/2)
			if delta < -1 || delta > 1 {
				t.Errorf("%s dark=%v account/update occupy different rows: %v / %v", stage, dark, account, update)
			}
			settings, hasSettings := tt.Find("设置")
			if !hasSettings || settings.X < account.X+account.W || settings.X+settings.W > update.X {
				t.Errorf("settings overlaps footer: %v / %v / %v", account, settings, update)
			}
			if update.X+update.W > sidebarWidth-12 || update.X < account.X+account.W {
				t.Errorf("%s footer overlaps or overflows: %v / %v", stage, account, update)
			}
		}
	}
}
func TestAccountEntryHasNoUsername(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	a := newApp(d, demoPlaces())
	tt := ui.NewTester(func(c *ui.Context) { a.drain(c); a.sync(c); a.accountRowView(c, colors(c)) }, 232, 50)
	tt.Frame()
	if tt.HasText(a.snap.Person(a.snap.Me).Name) {
		t.Error("sidebar account still displays username")
	}
	if _, ok := tt.Find("账号"); !ok {
		t.Error("account accessibility label missing")
	}
}

func TestSettingsContainsVersionPrivacyAndAccountManagement(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	a := newApp(d, demoPlaces())
	a.updates = newAppUpdater(a)
	a.updates.check = func(context.Context) (*appRelease, error) { return nil, nil }
	var opened string
	a.openURL = func(u string) error { opened = u; return nil }
	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Click("账号")
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	if !tt.HasText("登出") || tt.HasText("隐私说明") || tt.HasText("注销账号") {
		t.Fatalf("account contains settings actions: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	tt.Click("设置")
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	if !a.settingsOpen || !tt.HasText("隐私说明") || !tt.HasText("注销账号") || !tt.HasText("Deneb "+mygo.App.Version()) {
		t.Fatalf("settings missing actions/version: %v", tt.Texts())
	}
	tt.Click("隐私说明")
	tt.Frame()
	if opened != privacyURL {
		t.Fatalf("privacy target=%s", opened)
	}
	tt.Click("关闭设置")
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	if a.settingsOpen {
		t.Fatal("settings did not close")
	}
	tt.Click("设置")
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Click("软件更新")
	tt.Frame()
	if !a.settingsOpen || a.updates.stage != updateChecking && a.updates.stage != updateCurrent || a.updates.open {
		t.Fatal("settings did not check updates inline")
	}
	a.updates.open = false
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	tt.Click("设置")
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Click("注销账号")
	tt.Frame()
	if a.settingsOpen || a.deleting == nil {
		t.Fatal("account deletion confirmation not opened")
	}
}

func TestRenameDialogRequiresExplicitConfirmation(t *testing.T) {
	for _, dark := range []bool{false, true} {
		d := client.NewDemo()
		d.Manual()
		a := newApp(d, demoPlaces())
		a.selected = string(d.Snapshot().Self)
		tt := ui.NewTester(a.view, 980, 640)
		tt.SetDark(dark)
		tt.Frame()
		tt.Advance(800 * time.Millisecond)
		original := d.Snapshot().Device(d.Snapshot().Self).Name
		tt.Click("重命名")
		tt.Frame()
		tt.Advance(300 * time.Millisecond)
		if _, ok := tt.Find("名称"); !ok || !tt.HasText("确认") || !tt.HasText("取消") {
			t.Fatalf("no explicit rename dialog: %v", tt.Texts())
		}
		tt.Type("我的新设备")
		tt.Frame()
		// Moving focus must not save; cancellation must leave the original intact.
		tt.Key(0, ui.KeyTab)
		tt.Frame()
		if d.Snapshot().Device(d.Snapshot().Self).Name != original {
			t.Fatal("blur saved rename")
		}
		tt.Click("取消")
		tt.Frame()
		tt.Advance(200 * time.Millisecond)
		if a.renaming != nil || d.Snapshot().Device(d.Snapshot().Self).Name != original {
			t.Fatal("cancel mutated device")
		}
		tt.Click("重命名")
		tt.Frame()
		tt.Advance(250 * time.Millisecond)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type("  ")
		tt.Frame()
		tt.Click("确认")
		tt.Frame()
		if a.renaming == nil || d.Snapshot().Device(d.Snapshot().Self).Name != original {
			t.Fatal("blank rename submitted")
		}
		tt.Click("名称")
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type("新名称")
		tt.Frame()
		tt.Click("确认")
		tt.Frame()
		if a.renaming != nil || d.Snapshot().Device(d.Snapshot().Self).Name != "新名称" {
			t.Fatalf("confirm did not save: %v", tt.Texts())
		}
	}
}
func TestMyDevicesEntryShowsEveryOwnedDevice(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	a := newApp(d, demoPlaces())
	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	tt.Advance(800 * time.Millisecond)
	tt.Click("我的设备")
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	if !a.myDevicesOpen {
		t.Fatal("my devices entry has no action")
	}
	for _, id := range d.Snapshot().Mine {
		if !tt.HasText(d.Snapshot().Device(id).Name) {
			t.Errorf("device %s missing", id)
		}
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !a.myDevicesOpen {
		t.Fatal("Esc dismissed the independent page")
	}
}
func TestAvatarDraftCancelAndConfirm(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	d.Snapshot().Features["deviceAvatar"] = true
	a := newApp(d, demoPlaces())
	a.openAvatar(d.Snapshot().Self)
	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	tt.Click("小猫")
	tt.Frame()
	tt.Click("玫瑰")
	tt.Frame()
	if len(d.Snapshot().Device(d.Snapshot().Self).Avatar) != 0 {
		t.Fatal("preset saved before confirmation")
	}
	draft := bytes.Clone(a.avatarEdit.draft)
	tt.Click("取消")
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if a.avatarEdit != nil || len(d.Snapshot().Device(d.Snapshot().Self).Avatar) != 0 {
		t.Fatal("avatar cancel mutated state")
	}
	a.openAvatar(d.Snapshot().Self)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Click("小猫")
	tt.Frame()
	tt.Click("玫瑰")
	tt.Frame()
	tt.Click("保存")
	tt.Frame()
	if a.avatarEdit != nil || !bytes.Equal(draft, d.Snapshot().Device(d.Snapshot().Self).Avatar) {
		t.Fatal("confirmed avatar did not persist")
	}
	a.openAvatar(d.Snapshot().Self)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Click("恢复默认")
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if a.confirming == nil || len(a.avatarEdit.draft) == 0 {
		t.Fatal("restore skipped confirmation")
	}
	tt.Click("确认恢复图标")
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if len(a.avatarEdit.draft) != 0 || len(d.Snapshot().Device(d.Snapshot().Self).Avatar) == 0 {
		t.Fatal("restore preview wrote without save")
	}
	tt.Click("保存")
	tt.Frame()
	if len(d.Snapshot().Device(d.Snapshot().Self).Avatar) != 0 {
		t.Fatal("restore save failed")
	}
}
func TestRemovalConfirmationCancelAndConfirm(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	a := newApp(d, demoPlaces())
	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	tt.Advance(800 * time.Millisecond)
	dev := a.device("game")
	a.post(func(c *ui.Context) { a.takeOut(c, dev, true, false) })
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	if a.confirming == nil || !d.Snapshot().LAN("weekend").Has("game") {
		t.Fatal("remove happened before confirmation")
	}
	tt.Click("取消")
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if !d.Snapshot().LAN("weekend").Has("game") {
		t.Fatal("cancel removed device")
	}
	a.post(func(c *ui.Context) { a.takeOut(c, dev, true, false) })
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Click("确认移出局域网")
	tt.Frame()
	if d.Snapshot().LAN("weekend").Has("game") {
		t.Fatal("confirmed remove did not run")
	}
}

func BenchmarkAvatarPalette(b *testing.B) {
	for i := 0; i < b.N; i++ {
		e := avatarEditor{palette: i % len(avatarPalettes)}
		e.loadChoices()
	}
}

type failingProfileStore struct {
	*client.Demo
	calls int
}

func (s *failingProfileStore) Do(_ context.Context, _ model.Op) (model.Result, error) {
	s.calls++
	return model.Result{}, &model.Error{Code: model.ErrForbidden, Message: "服务器拒绝修改"}
}
func TestRenameFailureRetainsDraftAndDoesNotDoubleSubmit(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	s := &failingProfileStore{Demo: d}
	a := newApp(s, demoPlaces())
	a.beginRename(model.Op{Kind: model.OpDeviceRename, Device: d.Snapshot().Self}, "旧名称")
	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	tt.Type("新名称")
	tt.Frame()
	tt.Click("确认")
	tt.Frame()
	if a.renaming == nil || a.renaming.name != "新名称" || !tt.HasText("服务器拒绝修改") || s.calls != 1 {
		t.Fatal("rename failure lost draft or error")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.renaming != nil {
		t.Fatal("Esc did not cancel")
	}
}
func TestAvatarUploadErrorPreservesPreview(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	d.Snapshot().Features["deviceAvatar"] = true
	a := newApp(d, demoPlaces())
	a.openAvatar(d.Snapshot().Self)
	a.pickAvatar = func() ([]byte, error) { return nil, errors.New("请选择 PNG 或 JPEG 图片") }
	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	tt.Click("日光")
	tt.Frame()
	draft := bytes.Clone(a.avatarEdit.draft)
	tt.Click("上传图片")
	tt.Frame()
	for i := 0; i < 100 && a.avatarEdit.busy; i++ {
		time.Sleep(time.Millisecond)
		tt.Frame()
	}
	if a.avatarEdit == nil || !bytes.Equal(draft, a.avatarEdit.draft) || !tt.HasText("请选择 PNG 或 JPEG 图片") {
		t.Fatal("upload failure lost preview or error")
	}
}
func TestPresetPicturesMeetServerBounds(t *testing.T) {
	for p := range avatarPalettes {
		for i := range avatarPatterns {
			data := presetAvatar(i, p)
			cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil || cfg.Width != 128 || cfg.Height != 128 {
				t.Fatalf("preset %d/%d invalid", p, i)
			}
			if _, err := proto.NormalizeDeviceAvatar(data); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestRenameValidationMatchesServerLimits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"", false}, {"  ", false}, {"新设备 🚀", true},
		{strings.Repeat("名", 40), true}, {strings.Repeat("名", 41), false},
		{"设备\n名称", false}, {"设备\ufffd", false},
	} {
		if got := renameError(tc.name) == ""; got != tc.valid {
			t.Errorf("renameError(%q) valid=%v, want %v", tc.name, got, tc.valid)
		}
	}
}

func TestMyDevicesLongNamesAndEmptyList(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	d.Snapshot().Features["deviceAvatar"] = true
	long := strings.Repeat("工作电脑", 10)
	d.Snapshot().Device(d.Snapshot().Mine[0]).Name = long
	a := newApp(d, demoPlaces())
	a.myDevicesOpen = true
	tt := ui.NewTester(func(c *ui.Context) { a.myDevicesPage(c, colors(c)) }, 980, 640)
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	name, ok := tt.Find(long)
	rename, hasRename := tt.Find("重命名" + long)
	if !ok || !hasRename || name.X+name.W > rename.X-8 {
		t.Fatalf("long device name exceeds text column: %v", name)
	}
	a.myDevicesOpen = false
	a.snap.Mine = nil
	a.myDevicesOpen = true
	tt.Frame()
	if !tt.HasText("暂无设备") {
		t.Fatalf("empty list not shown: %v", tt.Texts())
	}
}

func TestSignOutClearsDeviceEditingState(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	d.Snapshot().Features["deviceAvatar"] = true
	a := newApp(d, demoPlaces())
	a.myDevicesOpen = true
	a.beginRename(model.Op{Kind: model.OpDeviceRename, Device: d.Snapshot().Self}, "旧名称")
	a.openAvatar(d.Snapshot().Self)
	a.askConfirm("删除设备", "旧设备", "", func(*ui.Context) { t.Fatal("stale action ran") })
	a.signedOut()
	if a.myDevicesOpen || a.renaming != nil || a.avatarEdit != nil || a.confirming != nil {
		t.Fatal("editing state survived sign-out")
	}
}

func TestNestedModalStaysCenteredDuringOpening(t *testing.T) {
	child := false
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Background(ui.Hex("#404040"))
		modalPanel(c, &lightPalette, "parent-position", "设备", 540, true, func() {}, func() { ui.Box(c).Height(250) })
		modalPanel(c, &lightPalette, "child-position", "重命名", 420, child, func() { child = false }, func() { ui.Box(c).Height(150) })
	}, 980, 640)
	tt.Frame()
	tt.Advance(time.Second)
	child = true
	tt.Frame()
	visible := 0
	for i := 0; i < 20; i++ {
		tt.Advance(16 * time.Millisecond)
		im := tt.Image()
		center := im.RGBAAt(490, 400).R
		// Compare the uniform blank surface to the darker parent, away from
		// inputs, titles and focus rings which are deliberately asymmetric.
		if int(center)-int(im.RGBAAt(235, 400).R) < 12 {
			continue
		}
		left, right := -1, -1
		for x := 220; x < 760; x++ {
			col := im.RGBAAt(x, 400)
			if int(col.R) >= int(center)-2 && col.G == col.R && col.B == col.R {
				if left < 0 {
					left = x
				}
				right = x
			}
		}
		if left < 0 || abs32(float32(left+right)/2-490) > 2 {
			t.Fatalf("frame %d: nested panel is off center: [%d,%d]", i, left, right)
		}
		visible++
	}
	if visible == 0 {
		t.Fatal("modal never became visible")
	}
}

func TestRenameCompositionCommitsOnlyOnConfirmation(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	a := newApp(d, demoPlaces())
	original := d.Snapshot().Device(d.Snapshot().Self).Name
	a.beginRename(model.Op{Kind: model.OpDeviceRename, Device: d.Snapshot().Self}, original)
	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Compose("新设备", 3)
	tt.Frame()
	if d.Snapshot().Device(d.Snapshot().Self).Name != original || a.renaming == nil {
		t.Fatal("IME composition saved without confirmation")
	}
	tt.Type("新设备")
	tt.Frame()
	if a.renaming.name != "新设备" {
		t.Fatalf("composition committed wrong draft: %q", a.renaming.name)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.renaming != nil || d.Snapshot().Device(d.Snapshot().Self).Name != "新设备" {
		t.Fatal("committed IME text was not saved by Enter")
	}
}

type pendingProfileStore struct {
	client.Store
	calls   chan model.Op
	release chan struct{}
}

func (s *pendingProfileStore) Do(ctx context.Context, op model.Op) (model.Result, error) {
	s.calls <- op
	select {
	case <-s.release:
		return model.Result{}, &model.Error{Code: model.ErrForbidden, Message: "服务器拒绝修改"}
	case <-ctx.Done():
		return model.Result{}, ctx.Err()
	}
}

func TestRenamePendingBlocksRepeatedSubmissionAndKeepsFailedDraft(t *testing.T) {
	d := client.NewDemo()
	d.Manual()
	s := &pendingProfileStore{Store: d, calls: make(chan model.Op, 4), release: make(chan struct{})}
	t.Cleanup(func() { close(s.release) })
	a := newApp(s, demoPlaces())
	a.beginRename(model.Op{Kind: model.OpDeviceRename, Device: d.Snapshot().Self}, "旧名称")
	tt := ui.NewTester(a.view, 980, 640)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Type("新名称")
	tt.Click("确认")
	tt.Frame()
	select {
	case <-s.calls:
	case <-time.After(time.Second):
		t.Fatal("request was not sent")
	}
	for range 3 {
		tt.Key(0, ui.KeyEnter)
		tt.Click("保存中…")
		tt.Frame()
	}
	if !a.renaming.saving || len(s.calls) != 0 {
		t.Fatal("pending rename submitted more than once")
	}
	s.release <- struct{}{}
	for i := 0; i < 100 && a.renaming.saving; i++ {
		time.Sleep(time.Millisecond)
		tt.Frame()
	}
	if a.renaming.saving || a.renaming.name != "新名称" || !tt.HasText("服务器拒绝修改") {
		t.Fatal("failed request did not retain editable draft")
	}
}
