package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/outbound"
)

// ------------------------------------------------------------------ Pushover

// DefaultPushoverAPI is the Pushover API address.
const DefaultPushoverAPI = "https://api.pushover.net"

type pushover struct {
	c        *outbound.Client
	endpoint string
	token    string
	user     string
	priority int
	device   string
	sound    string
}

func newPushover(c *outbound.Client, s *ServiceSettings, sec *Secrets) (*pushover, error) {
	if len(sec.Token) != 30 || len(sec.UserKey) != 30 {
		return nil, configErr("Pushover needs the application API token and the user (or group) key, 30 characters each")
	}
	p := &pushover{c: c, token: sec.Token, user: sec.UserKey, device: strings.TrimSpace(s.Device), sound: strings.TrimSpace(s.Sound)}
	if s.Priority != nil {
		// 2 (emergency) needs acknowledgement parameters: not offered.
		if *s.Priority < -2 || *s.Priority > 1 {
			return nil, configErr("the Pushover priority must be between -2 and 1")
		}
		p.priority = *s.Priority
	}
	api := DefaultPushoverAPI
	if s.ServerURL != "" {
		var err error
		if api, err = requireURL(s.ServerURL, "Pushover API URL"); err != nil {
			return nil, err
		}
	}
	p.endpoint = strings.TrimRight(api, "/") + "/1/messages.json"
	return p, nil
}

func (p *pushover) Test(ctx context.Context) error { return p.Send(ctx, TestMessage(time.Now())) }

// pushoverForm builds the form of POST /1/messages.json (limits: title 250,
// message 1024, url 512, url_title 100 characters).
func pushoverForm(m *Message, token, user string, priority int, device, sound string) url.Values {
	body := m.Body
	for _, f := range m.Fields {
		body += "\n" + f.Label + ": " + f.Value
	}
	if m.Problem && priority < 1 {
		priority++
	}
	f := url.Values{
		"token": {token}, "user": {user}, "title": {truncate(m.Title, 250)}, "message": {truncate(body, 1024)},
		"priority": {strconv.Itoa(priority)}, "timestamp": {strconv.FormatInt(m.Time.Unix(), 10)},
	}
	if m.Link != "" && len(m.Link) <= 512 {
		f.Set("url", m.Link)
		f.Set("url_title", "Open in Gonema")
	}
	if device != "" {
		f.Set("device", device)
	}
	if sound != "" {
		f.Set("sound", sound)
	}
	return f
}

func (p *pushover) Send(ctx context.Context, m *Message) error {
	form := pushoverForm(m, p.token, p.user, p.priority, p.device, p.sound)
	resp, err := p.c.Do(ctx, http.MethodPost, p.endpoint,
		http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, []byte(form.Encode()))
	if err != nil {
		return err
	}
	var r struct {
		Status int      `json:"status"`
		Errors []string `json:"errors"`
	}
	_ = json.Unmarshal(resp.Body, &r) // details only
	if err := p.c.Check(resp, strings.Join(r.Errors, "; ")); err != nil {
		return err
	}
	if r.Status != 1 {
		return p.c.Fail(outbound.ErrStatus, resp.Status, strings.Join(r.Errors, "; "))
	}
	return nil
}

// ------------------------------------------------------------------ ntfy

// DefaultNtfyServer is the public ntfy server.
const DefaultNtfyServer = "https://ntfy.sh"

type ntfy struct {
	c        *outbound.Client
	server   string
	topic    string
	priority int
	tags     []string
	auth     string
}

func newNtfy(c *outbound.Client, s *ServiceSettings, sec *Secrets) (*ntfy, error) {
	topic := strings.TrimSpace(s.Topic)
	if topic == "" || len(topic) > 64 || strings.ContainsAny(topic, "/?#&= \t") {
		return nil, configErr("the ntfy topic is required: up to 64 letters, digits, - or _")
	}
	server := DefaultNtfyServer
	if s.ServerURL != "" {
		var err error
		if server, err = requireURL(s.ServerURL, "ntfy server URL"); err != nil {
			return nil, err
		}
	}
	n := &ntfy{c: c, server: strings.TrimRight(server, "/"), topic: topic, priority: 3}
	if s.Priority != nil {
		if *s.Priority < 1 || *s.Priority > 5 {
			return nil, configErr("the ntfy priority must be between 1 (min) and 5 (max)")
		}
		n.priority = *s.Priority
	}
	for _, t := range s.Tags {
		if t = strings.TrimSpace(t); t != "" {
			n.tags = append(n.tags, truncate(t, 64))
		}
	}
	switch {
	case sec.Token != "":
		n.auth = "Bearer " + sec.Token
	case s.Username != "" || sec.Password != "":
		req := http.Request{Header: http.Header{}}
		req.SetBasicAuth(s.Username, sec.Password)
		n.auth = req.Header.Get("Authorization")
	}
	return n, nil
}

type ntfyBody struct {
	Topic    string   `json:"topic"`
	Title    string   `json:"title"`
	Message  string   `json:"message"`
	Priority int      `json:"priority"`
	Tags     []string `json:"tags,omitempty"`
	Click    string   `json:"click,omitempty"`
	Icon     string   `json:"icon,omitempty"`
}

// ntfyPayload builds the JSON publish body (POST to the server root): no
// header encoding issue with non-ASCII titles.
func ntfyPayload(m *Message, topic string, priority int, tags []string) ntfyBody {
	body := m.Body
	for _, f := range m.Fields {
		body += "\n" + f.Label + ": " + f.Value
	}
	if m.Problem && priority < 5 {
		priority++
	}
	return ntfyBody{Topic: topic, Title: m.Title, Message: body, Priority: priority, Tags: tags, Click: m.Link, Icon: m.PosterURL}
}

func (n *ntfy) Test(ctx context.Context) error { return n.Send(ctx, TestMessage(time.Now())) }

func (n *ntfy) Send(ctx context.Context, m *Message) error {
	body, err := json.Marshal(ntfyPayload(m, n.topic, n.priority, n.tags))
	if err != nil {
		return n.c.Fail(outbound.ErrResponse, 0, "cannot encode the message")
	}
	h := http.Header{"Content-Type": {"application/json"}}
	if n.auth != "" {
		h.Set("Authorization", n.auth)
	}
	resp, err := n.c.Do(ctx, http.MethodPost, n.server+"/", h, body)
	if err != nil {
		return err
	}
	var r struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(resp.Body, &r)
	return n.c.Check(resp, r.Error)
}

// ------------------------------------------------------------------ Gotify

type gotify struct {
	c        *outbound.Client
	endpoint string
	token    string
	priority int
}

func newGotify(c *outbound.Client, s *ServiceSettings, sec *Secrets) (*gotify, error) {
	server, err := requireURL(s.ServerURL, "Gotify server URL")
	if err != nil {
		return nil, err
	}
	if sec.Token == "" {
		return nil, configErr("the Gotify application token is required")
	}
	g := &gotify{c: c, endpoint: strings.TrimRight(server, "/") + "/message", token: sec.Token, priority: 5}
	if s.Priority != nil {
		if *s.Priority < 0 || *s.Priority > 10 {
			return nil, configErr("the Gotify priority must be between 0 and 10")
		}
		g.priority = *s.Priority
	}
	return g, nil
}

type gotifyBody struct {
	Title    string         `json:"title"`
	Message  string         `json:"message"`
	Priority int            `json:"priority"`
	Extras   map[string]any `json:"extras,omitempty"`
}

func gotifyPayload(m *Message, priority int) gotifyBody {
	body := m.Body
	for _, f := range m.Fields {
		body += "\n" + f.Label + ": " + f.Value
	}
	if m.Problem {
		priority = min(priority+2, 10)
	}
	b := gotifyBody{Title: m.Title, Message: body, Priority: priority}
	notif := map[string]any{}
	if m.Link != "" {
		notif["click"] = map[string]string{"url": m.Link}
	}
	if m.PosterURL != "" {
		notif["bigImageUrl"] = m.PosterURL
	}
	if len(notif) > 0 {
		b.Extras = map[string]any{"client::notification": notif}
	}
	return b
}

func (g *gotify) Test(ctx context.Context) error { return g.Send(ctx, TestMessage(time.Now())) }

func (g *gotify) Send(ctx context.Context, m *Message) error {
	body, err := json.Marshal(gotifyPayload(m, g.priority))
	if err != nil {
		return g.c.Fail(outbound.ErrResponse, 0, "cannot encode the message")
	}
	resp, err := g.c.Do(ctx, http.MethodPost, g.endpoint,
		http.Header{"Content-Type": {"application/json"}, "X-Gotify-Key": {g.token}}, body)
	if err != nil {
		return err
	}
	var r struct {
		ErrorDescription string `json:"errorDescription"`
	}
	_ = json.Unmarshal(resp.Body, &r)
	return g.c.Check(resp, r.ErrorDescription)
}
