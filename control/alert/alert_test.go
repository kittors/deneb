package alert

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// hook is a fake bot: it keeps what it was sent and answers as told.
type hook struct {
	mu     sync.Mutex
	got    []hookRequest
	answer string
	status int
}

type hookRequest struct {
	Path  string
	Query url.Values
	Body  map[string]any
}

func newHook(t *testing.T, answer string) (*hook, *httptest.Server) {
	h := &hook{answer: answer, status: 200}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		h.mu.Lock()
		h.got = append(h.got, hookRequest{r.URL.Path, r.URL.Query(), m})
		status, answer := h.status, h.answer
		h.mu.Unlock()
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			http.Error(w, "want json", 415)
			return
		}
		w.WriteHeader(status)
		io.WriteString(w, answer)
	}))
	t.Cleanup(ts.Close)
	return h, ts
}

func (h *hook) last(t *testing.T) hookRequest {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.got) == 0 {
		t.Fatal("nothing was posted")
	}
	return h.got[len(h.got)-1]
}

var testAlert = Alert{Key: "relay-offline/r_x", Kind: Firing, Title: "公共中转站「香港」连不上了", Text: "03:12 起没有再同步",
	Source: "deneb-control（203.0.113.10）", At: time.Date(2026, 11, 11, 1, 30, 0, 0, time.UTC)}

func str(m map[string]any, path ...string) string {
	var v any = m
	for _, p := range path {
		mm, _ := v.(map[string]any)
		v = mm[p]
	}
	s, _ := v.(string)
	return s
}

// TestKinds: each kind posts the format its service takes, signs where
// it signs, and tells a refusal from a success.
func TestKinds(t *testing.T) {
	ctx := context.Background()
	at := time.Unix(1_700_000_000, 0)
	want := Message(testAlert)
	if !strings.HasPrefix(want, "【Deneb 警报】公共中转站「香港」连不上了\n03:12 起没有再同步\n") || !strings.HasSuffix(want, "11-11 09:30（北京时间）") {
		t.Fatalf("the message:\n%s", want)
	}
	for _, tc := range []struct {
		kind, ok, refused, secret, chat string
		check                           func(t *testing.T, r hookRequest)
	}{
		{WeCom, `{"errcode":0,"errmsg":"ok"}`, `{"errcode":93000,"errmsg":"invalid webhook url"}`, "", "", func(t *testing.T, r hookRequest) {
			if str(r.Body, "msgtype") != "text" || str(r.Body, "text", "content") != want {
				t.Errorf("wecom: %v", r.Body)
			}
		}},
		{Feishu, `{"code":0,"msg":"success"}`, `{"code":19021,"msg":"sign match fail"}`, "fs-secret", "", func(t *testing.T, r hookRequest) {
			ts := str(r.Body, "timestamp")
			h := hmac.New(sha256.New, []byte(ts+"\n"+"fs-secret"))
			if str(r.Body, "msg_type") != "text" || str(r.Body, "content", "text") != want || ts != "1700000000" ||
				str(r.Body, "sign") != base64.StdEncoding.EncodeToString(h.Sum(nil)) {
				t.Errorf("feishu: %v", r.Body)
			}
		}},
		{DingTalk, `{"errcode":0,"errmsg":"ok"}`, `{"errcode":310000,"errmsg":"sign not match"}`, "dt-secret", "", func(t *testing.T, r hookRequest) {
			ts := r.Query.Get("timestamp")
			h := hmac.New(sha256.New, []byte("dt-secret"))
			h.Write([]byte(ts + "\n" + "dt-secret"))
			if str(r.Body, "text", "content") != want || ts != "1700000000000" || r.Query.Get("access_token") != "SECRETTOK" ||
				r.Query.Get("sign") != base64.StdEncoding.EncodeToString(h.Sum(nil)) {
				t.Errorf("dingtalk: %v %v", r.Query, r.Body)
			}
		}},
		{Telegram, `{"ok":true,"result":{}}`, `{"ok":false,"description":"chat not found"}`, "", "-1001", func(t *testing.T, r hookRequest) {
			if str(r.Body, "chat_id") != "-1001" || str(r.Body, "text") != want {
				t.Errorf("telegram: %v", r.Body)
			}
		}},
		{Bark, `{"code":200,"message":"success"}`, `{"code":400,"message":"failed to get device token"}`, "", "", func(t *testing.T, r hookRequest) {
			if str(r.Body, "title") != "【Deneb 警报】公共中转站「香港」连不上了" || !strings.HasPrefix(str(r.Body, "body"), "03:12 起没有再同步\n") ||
				str(r.Body, "group") != "Deneb" || str(r.Body, "level") != "timeSensitive" {
				t.Errorf("bark: %v", r.Body)
			}
		}},
		{Webhook, `{}`, "", "", "", func(t *testing.T, r hookRequest) {
			if str(r.Body, "key") != testAlert.Key || str(r.Body, "kind") != "firing" || str(r.Body, "title") != testAlert.Title || str(r.Body, "source") != testAlert.Source {
				t.Errorf("webhook: %v", r.Body)
			}
		}},
	} {
		h, ts := newHook(t, tc.ok)
		s, err := NewSender(Config{Kind: tc.kind, URL: ts.URL + "/hook/path?access_token=SECRETTOK", Secret: tc.secret, ChatID: tc.chat})
		if err != nil {
			t.Fatal(err)
		}
		s.now = func() time.Time { return at }
		if err := s.Send(ctx, testAlert); err != nil {
			t.Fatalf("%s: %v", tc.kind, err)
		}
		tc.check(t, h.last(t))
		if tc.refused != "" {
			h.mu.Lock()
			h.answer = tc.refused
			h.mu.Unlock()
			if err := s.Send(ctx, testAlert); err == nil {
				t.Errorf("%s: a refusal counted as sent", tc.kind)
			}
		}
		h.mu.Lock()
		h.status = 500
		h.mu.Unlock()
		if err := s.Send(ctx, testAlert); err == nil || strings.Contains(err.Error(), "SECRETTOK") {
			t.Errorf("%s: a 500: %v", tc.kind, err)
		}
	}
}

// TestSenderChecks: the settings are checked, the kind follows the host,
// and an error never carries the URL's key.
func TestSenderChecks(t *testing.T) {
	for u, kind := range map[string]string{
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=KEYVALUE": WeCom,
		"https://open.feishu.cn/open-apis/bot/v2/hook/KEYVALUE":         Feishu,
		"https://oapi.dingtalk.com/robot/send?access_token=KEYVALUE":    DingTalk,
		"https://api.day.app/KEYVALUE":                                  Bark,
		"https://hooks.example.com/deneb/KEYVALUE":                      Webhook,
	} {
		s, err := NewSender(Config{URL: u})
		if err != nil || s.kind != kind {
			t.Errorf("%s: %v %v", u, s, err)
		}
		if s != nil && strings.Contains(s.String(), "KEYVALUE") {
			t.Errorf("%s: the log line %q carries the key", u, s)
		}
	}
	for _, c := range []Config{
		{URL: "http://hooks.example.com/x"}, // not https
		{URL: "not a url"},
		{URL: "https://api.telegram.org/botX/sendMessage"}, // no chat
		{URL: "https://hooks.example.com/x", Kind: "pager"},
	} {
		if _, err := NewSender(c); err == nil {
			t.Errorf("%+v was taken", c)
		}
	}
	s, _ := NewSender(Config{URL: "http://127.0.0.1:1/cgi-bin/webhook/send?key=SECRETKEY", Kind: WeCom})
	if err := s.Send(context.Background(), testAlert); err == nil || strings.Contains(err.Error(), "SECRETKEY") {
		t.Fatalf("a closed port: %v", err)
	}
}

// TestLoadConfig: the file, the environment over it; a file open to
// others is closed.
func TestLoadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alert.json")
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if c, err := LoadConfig(path, getenv); err != nil || c.URL != "" {
		t.Fatalf("no file: %+v %v", c, err)
	}
	os.WriteFile(path, []byte(`{"kind": "feishu", "url": "https://open.feishu.cn/open-apis/bot/v2/hook/k", "secret": "s"}`), 0o644)
	if c, err := LoadConfig(path, getenv); err != nil || c.Kind != Feishu || c.Secret != "s" {
		t.Fatalf("the file: %+v %v", c, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file is %v", fi.Mode())
	}
	env["DENEB_ALERT_URL"], env["DENEB_ALERT_KIND"] = "https://api.day.app/k2", Bark
	if c, _ := LoadConfig(path, getenv); c.URL != "https://api.day.app/k2" || c.Kind != Bark || c.Secret != "s" {
		t.Fatalf("the environment: %+v", c)
	}
	os.WriteFile(path, []byte(`{`), 0o600)
	if _, err := LoadConfig(path, func(string) string { return "" }); err == nil {
		t.Fatal("a broken file")
	}
}

// recorder is a Sender that keeps what it sends, and fails when told.
type recorder struct {
	mu   sync.Mutex
	sent []Alert
	fail bool
}

func (r *recorder) Send(_ context.Context, a Alert) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return errors.New("down")
	}
	r.sent = append(r.sent, a)
	return nil
}

func (r *recorder) titles() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var s []string
	for _, a := range r.sent {
		s = append(s, string(a.Kind)+":"+a.Title)
	}
	return strings.Join(s, " | ")
}

// TestAlerter: a problem goes out once and its end once; a flap within the
// cooldown says nothing more, unless it still holds after the cooldown; a
// one-off waits out its cooldown; everything is kept, sent or not.
func TestAlerter(t *testing.T) {
	now := time.Date(2026, 11, 11, 1, 0, 0, 0, time.UTC)
	rec := &recorder{}
	var logged []string
	a := &Alerter{Source: "test", Sender: rec, Now: func() time.Time { return now },
		Logf: func(f string, args ...any) { logged = append(logged, fmt.Sprintf(f, args...)) }}

	a.Fire("r", "down", "")
	a.Fire("r", "down", "") // still: nothing
	now = now.Add(time.Minute)
	a.Resolve("r", "up", "")
	a.Resolve("r", "up", "") // not firing: nothing
	if got := rec.titles(); got != "firing:down | resolved:up" {
		t.Fatalf("down, up: %s", got)
	}
	// It flaps: held back, and quietly over.
	now = now.Add(time.Minute)
	a.Fire("r", "down", "again")
	a.Resolve("r", "up", "")
	// It flaps and stays down: said once the cooldown is past.
	a.Fire("r", "down", "again")
	a.Tick()
	if got := rec.titles(); got != "firing:down | resolved:up" {
		t.Fatalf("within the cooldown: %s", got)
	}
	if len(a.Active()) != 1 {
		t.Fatalf("active: %+v", a.Active())
	}
	now = now.Add(DefaultCooldown)
	a.Tick()
	a.Tick()
	now = now.Add(time.Minute)
	a.Resolve("r", "up", "")
	if got := rec.titles(); got != "firing:down | resolved:up | firing:down | resolved:up" {
		t.Fatalf("after the cooldown: %s", got)
	}
	if rec.sent[2].Text != "again（从 11-11 09:02（北京时间） 起）" {
		t.Fatalf("a late firing says since when: %q", rec.sent[2].Text)
	}

	// One-offs.
	a.Notify("start", "started", "")
	a.Notify("start", "started", "")
	now = now.Add(DefaultCooldown)
	a.Notify("start", "started", "")
	if n := strings.Count(rec.titles(), "info:started"); n != 2 {
		t.Fatalf("one-offs: %s", rec.titles())
	}

	// A failure is kept as one; with no sender, logged only.
	rec.fail = true
	a.Notify("x", "x", "")
	a2 := &Alerter{Source: "test", Now: func() time.Time { return now }}
	a2.Fire("y", "y", "")
	recent := a.Recent()
	if last := recent[len(recent)-1]; last.Outcome != Failed || last.Error != "down" {
		t.Fatalf("a failure: %+v", last)
	}
	if r := a2.Recent(); len(r) != 1 || r[0].Outcome != Logged {
		t.Fatalf("no sender: %+v", r)
	}
	held := 0
	for _, r := range recent {
		if r.Outcome == Held {
			held++
		}
	}
	if held != 4 || len(logged) != len(recent) || !strings.Contains(logged[0], "alert firing r: down [sent]") {
		t.Fatalf("held %d, logged %d of %d: %v", held, len(logged), len(recent), logged[:2])
	}
	for range keep + 10 {
		a.Notify("x", "x", "")
	}
	if len(a.Recent()) != keep {
		t.Fatalf("kept %d", len(a.Recent()))
	}
}
