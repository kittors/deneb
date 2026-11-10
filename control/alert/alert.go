// Package alert tells the deployment's operator when something goes wrong
// (design/plan/m7/control-ops.md §3): the control server about itself and
// the public relays, the watchdog (cmd/deneb-watchdog) about the other
// machine. Alerts go to a group bot (WeCom, Feishu, DingTalk), Telegram,
// Bark or any JSON webhook, as one setting picks; with none set they are
// only logged. Either way the last ones are kept for a look.
package alert

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Kind is what an alert says.
type Kind string

const (
	Firing   Kind = "firing"   // something went wrong
	Resolved Kind = "resolved" // and is right again
	Info     Kind = "info"     // worth knowing: a start, the daily "still watching"
)

// Alert is one message.
type Alert struct {
	Key    string    `json:"key"` // what it is about, for the cooldown: "relay-offline/r_x"
	Kind   Kind      `json:"kind"`
	Title  string    `json:"title"`          // "公共中转站「香港」连不上了"
	Text   string    `json:"text,omitempty"` // the details, a line or two
	Source string    `json:"source"`         // who says it: "deneb-control（203.0.113.10）"
	At     time.Time `json:"at"`
}

// Outcomes of an alert.
const (
	Sent   = "sent"   // it went out
	Held   = "held"   // the cooldown held it back
	Logged = "logged" // nowhere to send it: logged only
	Failed = "failed" // sending failed (Record.Error says why)
)

// Record is an alert and what became of it.
type Record struct {
	Alert
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
}

// Sender sends an alert somewhere.
type Sender interface {
	Send(ctx context.Context, a Alert) error
}

// DefaultCooldown is how long the same thing waits before it is said
// again.
const DefaultCooldown = 30 * time.Minute

// keep is how many records Recent keeps.
const keep = 200

// sendTimeout bounds one send.
const sendTimeout = 15 * time.Second

// Alerter decides what goes out. A problem (Fire) goes out once, and its
// end (Resolve) once; the same key says nothing again within the cooldown,
// so a flapping relay is no flood. A problem the cooldown held back goes
// out at the next Tick after it, if it still holds, so it is never left
// unsaid. One-offs (Notify) go out unless their key went out within the
// cooldown. Every alert is logged and kept for Recent, sent or not.
type Alerter struct {
	Source   string
	Sender   Sender // nil: logged only
	Logf     func(format string, args ...any)
	Now      func() time.Time
	Cooldown time.Duration // 0: DefaultCooldown

	mu     sync.Mutex
	active map[string]*firing
	last   map[string]time.Time // when each key last went out
	recent []Record
}

type firing struct {
	alert Alert
	sent  bool // its firing went out
}

func (a *Alerter) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Alerter) cooldown() time.Duration {
	if a.Cooldown > 0 {
		return a.Cooldown
	}
	return DefaultCooldown
}

func (a *Alerter) init() {
	if a.active == nil {
		a.active, a.last = map[string]*firing{}, map[string]time.Time{}
	}
}

// due reports whether key may go out now; the caller holds a.mu.
func (a *Alerter) due(key string, now time.Time) bool {
	last, ok := a.last[key]
	return !ok || now.Sub(last) >= a.cooldown()
}

// Fire says something is wrong, and stays quiet while it stays wrong.
func (a *Alerter) Fire(key, title, text string) {
	a.mu.Lock()
	a.init()
	if a.active[key] != nil {
		a.mu.Unlock()
		return
	}
	now := a.now()
	al := Alert{Key: key, Kind: Firing, Title: title, Text: text, Source: a.Source, At: now}
	f := &firing{alert: al}
	a.active[key] = f
	send := a.due(key, now)
	if send {
		f.sent, a.last[key] = true, now
	}
	a.mu.Unlock()
	a.deliver(al, send)
}

// Resolve says the problem under key is over: aloud if its firing went
// out, quietly (kept, logged) if the cooldown held the firing back.
func (a *Alerter) Resolve(key, title, text string) {
	a.mu.Lock()
	a.init()
	f := a.active[key]
	if f == nil {
		a.mu.Unlock()
		return
	}
	delete(a.active, key)
	now := a.now()
	if f.sent {
		a.last[key] = now
	}
	a.mu.Unlock()
	a.deliver(Alert{Key: key, Kind: Resolved, Title: title, Text: text, Source: a.Source, At: now}, f.sent)
}

// Notify says something once, unless key went out within the cooldown.
func (a *Alerter) Notify(key, title, text string) {
	a.mu.Lock()
	a.init()
	now := a.now()
	send := a.due(key, now)
	if send {
		a.last[key] = now
	}
	a.mu.Unlock()
	a.deliver(Alert{Key: key, Kind: Info, Title: title, Text: text, Source: a.Source, At: now}, send)
}

// Tick sends the firings the cooldown held back that still hold. Call it
// every minute or so.
func (a *Alerter) Tick() {
	a.mu.Lock()
	a.init()
	now := a.now()
	var out []Alert
	for key, f := range a.active {
		if !f.sent && a.due(key, now) {
			f.sent, a.last[key] = true, now
			al := f.alert
			al.At, al.Text = now, fmt.Sprintf("%s（从 %s 起）", al.Text, Clock(f.alert.At))
			out = append(out, al)
		}
	}
	a.mu.Unlock()
	slices.SortFunc(out, func(x, y Alert) int { return cmp.Compare(x.Key, y.Key) })
	for _, al := range out {
		a.deliver(al, true)
	}
}

// Active are the problems firing now, oldest first.
func (a *Alerter) Active() []Alert {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []Alert
	for _, f := range a.active {
		out = append(out, f.alert)
	}
	slices.SortFunc(out, func(x, y Alert) int { return x.At.Compare(y.At) })
	return out
}

// Recent are the last alerts and what became of them, oldest first.
func (a *Alerter) Recent() []Record {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.recent)
}

// deliver logs al, sends it if send and there is a sender, and keeps the
// record.
func (a *Alerter) deliver(al Alert, send bool) {
	r := Record{Alert: al, Outcome: Held}
	switch {
	case send && a.Sender == nil:
		r.Outcome = Logged
	case send:
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		err := a.Sender.Send(ctx, al)
		cancel()
		r.Outcome = Sent
		if err != nil {
			r.Outcome, r.Error = Failed, err.Error()
		}
	}
	if a.Logf != nil {
		line := fmt.Sprintf("alert %s %s: %s", al.Kind, al.Key, al.Title)
		if al.Text != "" {
			line += " — " + al.Text
		}
		line += " [" + r.Outcome
		if r.Error != "" {
			line += ": " + r.Error
		}
		a.Logf("%s]", line)
	}
	a.mu.Lock()
	a.recent = append(a.recent, r)
	if len(a.recent) > keep {
		a.recent = slices.Delete(a.recent, 0, len(a.recent)-keep)
	}
	a.mu.Unlock()
}

// beijing is the time people read the alerts in: UTC+8 has no daylight
// saving, and a fixed zone needs no tzdata in an empty image.
var beijing = time.FixedZone("UTC+8", 8*3600)

// Clock is t as a message says it: "11-11 09:30（北京时间）".
func Clock(t time.Time) string {
	return t.In(beijing).Format("01-02 15:04") + "（北京时间）"
}

// Message is an alert as plain text, for the chat bots:
//
//	【Deneb 警报】公共中转站「香港」连不上了
//	03:12 起没有再同步
//	deneb-control（203.0.113.10）· 11-11 09:30（北京时间）
func Message(a Alert) string {
	head := map[Kind]string{Firing: "【Deneb 警报】", Resolved: "【Deneb 恢复】", Info: "【Deneb】"}[a.Kind]
	s := head + a.Title + "\n"
	if a.Text != "" {
		s += a.Text + "\n"
	}
	return s + a.Source + " · " + Clock(a.At)
}
