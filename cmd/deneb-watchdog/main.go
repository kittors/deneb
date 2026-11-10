// Command deneb-watchdog watches the other machine of a Deneb deployment
// and tells the operator when something there goes wrong
// (design/plan/m7/control-ops.md §3): the control server's /healthz, the
// relays (DERP over TLS, and STUN), the backups being fresh, the disk.
// The control server tells about itself (control/health.go); what it
// cannot tell, being down above all, this does, from the other machine.
//
// It listens on nothing: every minute it makes its checks, outbound only,
// and sends an alert after two failures in a row, and again when the
// check passes. Each morning it says it is still watching, so a watchdog
// that died is missed by noon.
//
//	deneb-watchdog -config /etc/deneb-watchdog/watch.json
//	deneb-watchdog -config watch.json -once      # one round, printed, no alerts
//	deneb-watchdog -config watch.json -validate  # only check the file
//
// The file (deploy/watchdog/watch.example.json) names the machine, where
// alerts go (as control/alert takes it; empty: the log only) and the
// checks. It holds addresses and the bot's key: it is the deployment's,
// never in the repository.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kittors/deneb/control/alert"
)

// Config is the watchdog's file.
type Config struct {
	// Name is the machine it runs on, as the messages say it: "hk-spare".
	Name  string       `json:"name"`
	Alert alert.Config `json:"alert"`
	// Every is how often the checks run; default 1m.
	Every string `json:"every,omitempty"`
	// Daily is when, Beijing time, it says it is still watching: "09:00"
	// (the default), or "off".
	Daily  string  `json:"daily,omitempty"`
	Checks []Check `json:"checks"`
}

// failAfter failures in a row make an alert: one lost packet is not one.
const failAfter = 2

func main() {
	var (
		path     = flag.String("config", "/etc/deneb-watchdog/watch.json", "the watchdog's file")
		once     = flag.Bool("once", false, "run the checks once, print the results, send nothing")
		validate = flag.Bool("validate", false, "only check the file")
	)
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.LUTC)
	log.SetPrefix("deneb-watchdog: ")
	w, err := load(*path, os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	if *validate {
		fmt.Printf("%s：%d 项检查，每 %s 一轮，提醒发到 %s\n", w.name, len(w.checks), w.every, w.sendsTo())
		return
	}
	if *once {
		ok := true
		for i, err := range w.round(context.Background()) {
			c := w.checks[i]
			if err != nil {
				ok = false
				fmt.Printf("✗ %s（%s）：%v\n", c.Name, c.Kind, err)
			} else {
				fmt.Printf("✓ %s（%s）\n", c.Name, c.Kind)
			}
		}
		if !ok {
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("%s: %d checks every %s; alerts to %s", w.name, len(w.checks), w.every, w.sendsTo())
	w.run(ctx)
}

// watchdog is a running watchdog.
type watchdog struct {
	name   string
	every  time.Duration
	daily  string // "15:04", or "" for none
	checks []Check
	alerts *alert.Alerter
	now    func() time.Time

	mu       sync.Mutex
	fails    []int
	lastDay  string // the Beijing date it last said it is watching
	sender   alert.Sender
	senderTo string
}

// load reads and checks the file. The alert's key may also come from the
// environment (DENEB_ALERT_*), as for the control server.
func load(path string, getenv func(string) string) (*watchdog, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Name == "" {
		return nil, errors.New("the file names no machine (name)")
	}
	w := &watchdog{name: c.Name, every: time.Minute, daily: "09:00", checks: c.Checks, now: time.Now, fails: make([]int, len(c.Checks))}
	if c.Every != "" {
		if w.every, err = time.ParseDuration(c.Every); err != nil || w.every < 10*time.Second {
			return nil, fmt.Errorf("every %q: want a duration of 10s or more", c.Every)
		}
	}
	switch c.Daily {
	case "":
	case "off":
		w.daily = ""
	default:
		if _, err := time.Parse("15:04", c.Daily); err != nil {
			return nil, fmt.Errorf("daily %q: want HH:MM or off", c.Daily)
		}
		w.daily = c.Daily
	}
	if len(c.Checks) == 0 {
		return nil, errors.New("the file has no checks")
	}
	names := map[string]bool{}
	for i := range w.checks {
		if err := w.checks[i].prepare(); err != nil {
			return nil, fmt.Errorf("check %d (%s): %w", i+1, w.checks[i].Name, err)
		}
		if names[w.checks[i].Name] {
			return nil, fmt.Errorf("two checks are called %q", w.checks[i].Name)
		}
		names[w.checks[i].Name] = true
	}
	for name, field := range map[string]*string{
		"DENEB_ALERT_KIND": &c.Alert.Kind, "DENEB_ALERT_URL": &c.Alert.URL, "DENEB_ALERT_SECRET": &c.Alert.Secret, "DENEB_ALERT_CHAT": &c.Alert.ChatID,
	} {
		if v := getenv(name); v != "" {
			*field = v
		}
	}
	w.senderTo = "只记日志"
	if c.Alert.URL != "" {
		s, err := alert.NewSender(c.Alert)
		if err != nil {
			return nil, err
		}
		w.sender, w.senderTo = s, s.String()
	}
	w.alerts = &alert.Alerter{Source: "deneb-watchdog（" + c.Name + "）", Sender: w.sender, Logf: log.Printf, Now: func() time.Time { return w.now() }}
	return w, nil
}

func (w *watchdog) sendsTo() string { return w.senderTo }

// round runs every check at once and returns their results, in order.
func (w *watchdog) round(ctx context.Context) []error {
	errs := make([]error, len(w.checks))
	var wg sync.WaitGroup
	for i := range w.checks {
		wg.Go(func() {
			cctx, cancel := context.WithTimeout(ctx, checkTimeout)
			defer cancel()
			errs[i] = w.checks[i].run(cctx)
		})
	}
	wg.Wait()
	return errs
}

// step is one round and what it says: an alert after failAfter failures in
// a row, its end at the next success; the morning's "still watching".
func (w *watchdog) step(ctx context.Context) {
	errs := w.round(ctx)
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, err := range errs {
		c := w.checks[i]
		key := "check/" + c.Name
		if err != nil {
			w.fails[i]++
			if w.fails[i] >= failAfter {
				w.alerts.Fire(key, c.Name+"出问题了", fmt.Sprintf("%v（连续 %d 次）", err, w.fails[i]))
			}
			continue
		}
		w.fails[i] = 0
		w.alerts.Resolve(key, c.Name+"好了", "")
	}
	w.alerts.Tick()
	if w.daily == "" {
		return
	}
	now := w.now().In(beijing)
	day := now.Format("2006-01-02")
	if now.Format("15:04") >= w.daily && w.lastDay != day {
		w.lastDay = day
		var bad []string
		for _, a := range w.alerts.Active() {
			bad = append(bad, strings.TrimSuffix(a.Title, "出问题了"))
		}
		text := fmt.Sprintf("%d 项都正常", len(w.checks))
		if len(bad) > 0 {
			text = fmt.Sprintf("%d 项里有问题的：%s", len(w.checks), strings.Join(bad, "、"))
		}
		w.alerts.Notify("daily/"+day, "看门狗还在看", text)
	}
}

// run says it started, then makes a round every w.every until ctx ends.
func (w *watchdog) run(ctx context.Context) {
	w.start()
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		w.step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// start says the watchdog started.
func (w *watchdog) start() {
	var names []string
	for _, c := range w.checks {
		names = append(names, c.Name)
	}
	// It started: a planned deploy, or a crash the restart policy caught.
	w.alerts.Notify("started", "看门狗开始看了", "看着 "+strings.Join(names, "、"))
	// Started past the morning's hour, it says so tomorrow; before it,
	// today.
	if now := w.now().In(beijing); w.daily != "" && now.Format("15:04") >= w.daily {
		w.lastDay = now.Format("2006-01-02")
	}
}

var beijing = time.FixedZone("UTC+8", 8*3600)
