package main

import (
	"cmp"
	"context"
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/engine/localapi/apitype"
)

// m2watch is the window watching M2's item 8, the network changed and the
// Mac asleep, done for real on the machine while it runs (DENEB_REAL2=watch,
// DENEB_WATCH=20m): the "全部" view on this computer's real engine, until
// DENEB_WATCH is over by the wall clock. Every change of what it says, each
// device's second line, the header's sentence, the menu bar panel's head
// and its mark, goes to DENEB_WATCH_LOG (/tmp/real2/watch.log) at its
// moment, with the engine's own path reports beside it; a small frame a
// second goes to DENEB_WATCH_FRAMES (/tmp/real2/watch), named by the time.
// It never gives up waiting: with the network off nothing answers, and
// asleep the process stands still while the wall clock runs on; a jump of
// the clock is logged as such. At the end it sums up: what each device
// said and for how long, any of the words that tell of trouble, and when
// each last came back to 直接连上 or 经…中转.
func m2watch(r *realRig) {
	dur, err := time.ParseDuration(os.Getenv("DENEB_WATCH"))
	if err != nil || dur <= 0 {
		r.t.Fatal("watch: set DENEB_WATCH to how long to watch, as 20m")
	}
	logPath := cmp.Or(os.Getenv("DENEB_WATCH_LOG"), "/tmp/real2/watch.log")
	frameDir := cmp.Or(os.Getenv("DENEB_WATCH_FRAMES"), "/tmp/real2/watch")
	if err := os.MkdirAll(frameDir, 0o755); err != nil {
		r.t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(logPath), 0o755)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		r.t.Fatal(err)
	}
	defer f.Close()
	var mu sync.Mutex
	logAt := func(at time.Time, format string, args ...any) {
		line := at.Format("15:04:05.000") + "  " + fmt.Sprintf(format, args...)
		mu.Lock()
		fmt.Fprintln(f, line)
		mu.Unlock()
		r.t.Log(line)
	}

	if !r.a.all() {
		r.click("全部")
		r.until("the 全部 view", r.a.all)
	}
	r.tt.SetScale(0.5)
	wall := func() time.Time { return time.Now().Round(0) } // the wall clock, which runs on in sleep
	start := wall()
	end := start.Add(dur)
	logAt(start, "==== 值守开始，看到 %s 为止；日志 %s，帧 %s", end.Format("15:04:05"), logPath, frameDir)

	// The engine's own reports of each path, as they come, beside what the
	// window makes of them; again after the engine comes back.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		last := map[apitype.DeviceID]apitype.PeerPath{}
		for ctx.Err() == nil {
			r.lc.Watch(ctx, func(e apitype.Event) error {
				paths := e.Paths
				if e.State != nil {
					paths = e.State.Paths
				}
				for _, p := range paths {
					if o, ok := last[p.DeviceID]; ok && o.Kind == p.Kind && o.Relay == p.Relay && o.Endpoint == p.Endpoint {
						continue
					}
					last[p.DeviceID] = p
					where := ""
					switch {
					case p.Relay != "":
						where = "，经 " + string(p.Relay)
					case p.Endpoint != "":
						where = "，" + p.Endpoint
					}
					logAt(wall(), "引擎：%s %s%s", p.DeviceID, p.Kind, where)
				}
				return nil
			})
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}()

	type span struct {
		text     string
		from, to time.Time
	}
	spans := map[string][]span{} // what each thing watched said, in turn
	labels := map[string]string{}
	var order []string
	bad := []string{"断开", "连不上", "出错", "失败", "错误", "没在运行", "不在线"}
	var troubles []string
	backAt := map[string]time.Time{} // when each device last came back to 直接连上 or 经…中转
	var jumps []string
	see := func(now time.Time, key, label, text string) {
		s := spans[key]
		if n := len(s); n > 0 && s[n-1].text == text {
			s[n-1].to = now
			return
		}
		if _, ok := labels[key]; !ok {
			labels[key] = label
			order = append(order, key)
		}
		was := "（第一次）"
		if n := len(s); n > 0 {
			was = "，原来是「" + s[n-1].text + "」"
			s[n-1].to = now
		}
		spans[key] = append(s, span{text: text, from: now, to: now})
		logAt(now, "%s：「%s」%s", label, text, was)
		for _, w := range bad {
			if strings.Contains(text, w) {
				troubles = append(troubles, fmt.Sprintf("%s %s：「%s」", now.Format("15:04:05.000"), label, text))
				break
			}
		}
		if text == "直接连上" || strings.HasPrefix(text, "经") && strings.HasSuffix(text, "中转") {
			backAt[key] = now
		}
	}

	last, lastFrame := start, time.Time{}
	for {
		now := wall()
		if gap := now.Sub(last); gap > 3*time.Second {
			jump := fmt.Sprintf("%s 起 %s（睡眠，或进程被冻住）", last.Format("15:04:05.000"), gap.Round(time.Millisecond))
			jumps = append(jumps, jump)
			logAt(now, "墙上时钟跳了：%s", jump)
			// A tester plays every frame of the time it is moved on, one by
			// one: ten minutes of a sleep took it half a minute, the log
			// standing still meanwhile (the test Mac, 20:30). A new one
			// starts at the wall clock, as a window's next frame after a
			// sleep is simply the next one.
			r.tt = ui.NewTester(r.a.view, 1180, 740)
			r.tt.SetDark(os.Getenv("DENEB_DARK") != "")
			r.tt.SetScale(0.5)
		} else {
			// The view's clock goes with the wall clock.
			r.tt.Advance(max(now.Sub(last), 0))
		}
		r.tt.Frame()
		last = now
		for _, d := range r.a.devices {
			if !d.gone.IsZero() {
				continue
			}
			label := d.name
			if l := r.a.snap.LAN(d.lan); l != nil {
				label += "（" + l.Name + "）"
			}
			see(now, "device/"+d.id, label, r.a.status(d))
		}
		_, header := r.a.summary(&lightPalette)
		see(now, "header", "标题", header)
		_, head := r.a.panelSummary(&lightPalette)
		see(now, "panel", "菜单栏面板", head)
		see(now, "mark", "菜单栏图标", map[string]string{"solid": "实心星", "dot": "带点的星", "hollow": "空心星"}[trayState(r.a.snap, now)])
		if now.Sub(lastFrame) >= time.Second {
			lastFrame = now
			if out, err := os.Create(filepath.Join(frameDir, now.Format("150405")+".jpg")); err == nil {
				jpeg.Encode(out, r.tt.Image(), &jpeg.Options{Quality: 70})
				out.Close()
			}
		}
		if !now.Before(end) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// The sum.
	stop := wall()
	var sum []string
	sum = append(sum, fmt.Sprintf("==== 值守结束（%s 到 %s）", start.Format("15:04:05"), stop.Format("15:04:05")))
	for _, key := range order {
		var kinds []string
		took := map[string]time.Duration{}
		times := map[string]int{}
		for _, s := range spans[key] {
			if _, ok := took[s.text]; !ok {
				kinds = append(kinds, s.text)
			}
			took[s.text] += s.to.Sub(s.from)
			times[s.text]++
		}
		var parts []string
		for _, k := range kinds {
			parts = append(parts, fmt.Sprintf("「%s」%d 次共 %s", k, times[k], took[k].Round(100*time.Millisecond)))
		}
		line := labels[key] + "：" + strings.Join(parts, "，")
		if at, ok := backAt[key]; ok && strings.HasPrefix(key, "device/") {
			line += "；最后一次回到连上：" + at.Format("15:04:05.000")
		}
		sum = append(sum, line)
	}
	if len(jumps) > 0 {
		sum = append(sum, "墙上时钟跳过 "+fmt.Sprint(len(jumps))+" 次："+strings.Join(jumps, "；"))
	} else {
		sum = append(sum, "墙上时钟没有跳过")
	}
	if len(troubles) > 0 {
		sum = append(sum, fmt.Sprintf("出现过 %d 次出错一类的说法：", len(troubles)))
		sum = append(sum, troubles...)
	} else {
		sum = append(sum, "没有出现过"+strings.Join(bad, "、")+"这类说法")
	}
	frames, _ := filepath.Glob(filepath.Join(frameDir, "*.jpg"))
	sum = append(sum, fmt.Sprintf("帧：%s，目前共 %d 张", frameDir, len(frames)))
	for _, line := range sum {
		logAt(stop, "%s", line)
	}
}
