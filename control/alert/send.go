package alert

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Where alerts go: one setting, its kind picking the message's format.
const (
	WeCom    = "wecom"    // 企业微信群机器人: https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=…
	Feishu   = "feishu"   // 飞书群机器人: https://open.feishu.cn/open-apis/bot/v2/hook/…, Secret if it signs
	DingTalk = "dingtalk" // 钉钉群机器人: https://oapi.dingtalk.com/robot/send?access_token=…, Secret if it signs
	Telegram = "telegram" // https://api.telegram.org/bot<token>/sendMessage, with ChatID
	Bark     = "bark"     // https://api.day.app/<key>, or a Bark server of one's own
	Webhook  = "webhook"  // any URL: the Alert as JSON
)

// Config is where alerts go. The URL carries the bot's key, so it is a
// secret like a password: it lives in a file only the server reads (or the
// environment), never in the repository.
type Config struct {
	Kind   string `json:"kind,omitempty"` // empty: from the URL's host, else webhook
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"` // Feishu's or DingTalk's signing secret
	ChatID string `json:"chatID,omitempty"` // Telegram's chat
}

// LoadConfig reads path (if it is there; a file open to others is made
// 0600, since it holds a key), then the environment over it:
// DENEB_ALERT_KIND, DENEB_ALERT_URL, DENEB_ALERT_SECRET, DENEB_ALERT_CHAT.
// URL empty means none is set.
func LoadConfig(path string, getenv func(string) string) (Config, error) {
	var c Config
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			if err := json.Unmarshal(b, &c); err != nil {
				return c, fmt.Errorf("%s: %w", path, err)
			}
			if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
				os.Chmod(path, 0o600)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return c, err
		}
	}
	for name, field := range map[string]*string{
		"DENEB_ALERT_KIND": &c.Kind, "DENEB_ALERT_URL": &c.URL, "DENEB_ALERT_SECRET": &c.Secret, "DENEB_ALERT_CHAT": &c.ChatID,
	} {
		if v := getenv(name); v != "" {
			*field = v
		}
	}
	return c, nil
}

// kindOf is the kind a URL's host says, for a Config that gives none.
func kindOf(u *url.URL) string {
	switch h := strings.ToLower(u.Hostname()); {
	case h == "qyapi.weixin.qq.com":
		return WeCom
	case h == "open.feishu.cn" || h == "open.larksuite.com":
		return Feishu
	case h == "oapi.dingtalk.com":
		return DingTalk
	case h == "api.telegram.org":
		return Telegram
	case h == "api.day.app":
		return Bark
	}
	return Webhook
}

// HTTPSender sends alerts as one of the kinds.
type HTTPSender struct {
	kind   string
	u      *url.URL
	secret string
	chat   string
	client *http.Client
	now    func() time.Time // for the signatures; tests set it
}

// NewSender checks c and makes its sender. The URL is https, or http to
// this machine only (a test, a relay of one's own on the same machine).
func NewSender(c Config) (*HTTPSender, error) {
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil || u.Host == "" {
		return nil, errors.New("alert: the URL is not a URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname())) {
		return nil, errors.New("alert: the URL must be https")
	}
	kind := c.Kind
	if kind == "" {
		kind = kindOf(u)
	}
	switch kind {
	case WeCom, Feishu, DingTalk, Bark, Webhook:
	case Telegram:
		if c.ChatID == "" {
			return nil, errors.New("alert: telegram needs chatID")
		}
	default:
		return nil, fmt.Errorf("alert: kind %q: want %s, %s, %s, %s, %s or %s", kind, WeCom, Feishu, DingTalk, Telegram, Bark, Webhook)
	}
	return &HTTPSender{kind: kind, u: u, secret: c.Secret, chat: c.ChatID,
		client: &http.Client{Timeout: sendTimeout}, now: time.Now}, nil
}

// String is where alerts go, without the key: for the log.
func (s *HTTPSender) String() string { return s.kind + " (" + s.u.Host + ")" }

// request is the alert as kind wants it: the URL to post to, and the body.
func (s *HTTPSender) request(a Alert) (string, []byte, error) {
	u := *s.u
	text := Message(a)
	var body any
	switch s.kind {
	case WeCom:
		body = map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}
	case Feishu:
		m := map[string]any{"msg_type": "text", "content": map[string]string{"text": text}}
		if s.secret != "" {
			// Feishu signs with the timestamp and the secret as the key.
			ts := strconv.FormatInt(s.now().Unix(), 10)
			h := hmac.New(sha256.New, []byte(ts+"\n"+s.secret))
			m["timestamp"], m["sign"] = ts, base64.StdEncoding.EncodeToString(h.Sum(nil))
		}
		body = m
	case DingTalk:
		body = map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}
		if s.secret != "" {
			// DingTalk signs the timestamp (ms) with the secret, in the URL.
			ts := strconv.FormatInt(s.now().UnixMilli(), 10)
			h := hmac.New(sha256.New, []byte(s.secret))
			h.Write([]byte(ts + "\n" + s.secret))
			q := u.Query()
			q.Set("timestamp", ts)
			q.Set("sign", base64.StdEncoding.EncodeToString(h.Sum(nil)))
			u.RawQuery = q.Encode()
		}
	case Telegram:
		body = map[string]any{"chat_id": s.chat, "text": text, "disable_web_page_preview": true}
	case Bark:
		head := map[Kind]string{Firing: "【Deneb 警报】", Resolved: "【Deneb 恢复】", Info: "【Deneb】"}[a.Kind]
		m := map[string]any{"title": head + a.Title, "body": strings.TrimPrefix(text, head+a.Title+"\n"), "group": "Deneb"}
		if a.Kind == Firing {
			m["level"] = "timeSensitive" // through Focus on iOS
		}
		body = m
	default:
		body = a
	}
	b, err := json.Marshal(body)
	return u.String(), b, err
}

// Send posts the alert and checks the service took it.
func (s *HTTPSender) Send(ctx context.Context, a Alert) error {
	target, body, err := s.request(a)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(body))
	if err != nil {
		return errors.New("alert: bad request")
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := s.client.Do(req)
	if err != nil {
		// The URL has the bot's key in it: say only the host.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("post to %s: %w", s.u.Host, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s answered %s: %s", s.u.Host, resp.Status, short(raw))
	}
	var r struct {
		ErrCode    *int  `json:"errcode"`    // WeCom, DingTalk
		Code       *int  `json:"code"`       // Feishu (0), Bark (200)
		StatusCode *int  `json:"StatusCode"` // Feishu, older
		OK         *bool `json:"ok"`         // Telegram
	}
	json.Unmarshal(raw, &r)
	ok := true
	switch s.kind {
	case WeCom, DingTalk:
		ok = r.ErrCode != nil && *r.ErrCode == 0
	case Feishu:
		ok = r.Code != nil && *r.Code == 0 || r.Code == nil && r.StatusCode != nil && *r.StatusCode == 0
	case Telegram:
		ok = r.OK != nil && *r.OK
	case Bark:
		ok = r.Code != nil && *r.Code == 200
	}
	if !ok {
		return fmt.Errorf("%s did not take it: %s", s.u.Host, short(raw))
	}
	return nil
}

func short(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
