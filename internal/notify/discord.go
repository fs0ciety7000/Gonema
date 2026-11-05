package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/fs0ciety7000/gonema/internal/outbound"
)

// Status colours of DESIGN.md (dark theme), as RGB integers for embeds.
const (
	colorDownloading = 0x5EC4D6
	colorMissing     = 0xE8AE55
	colorUpgraded    = 0xD69AEA
	colorFailed      = 0xF27268
	colorAvailable   = 0x7FCC89
	colorNeutral     = 0x8A8F98
)

func colorOf(m *Message) int {
	switch {
	case m.Problem && m.Event == EventHealth:
		return colorMissing
	case m.Problem:
		return colorFailed
	}
	switch m.Event {
	case EventGrab:
		return colorDownloading
	case EventImport:
		return colorAvailable
	case EventUpgrade:
		return colorUpgraded
	case EventHealth:
		return colorAvailable
	}
	return colorNeutral
}

type discord struct {
	c        *outbound.Client
	url      string
	username string
}

func newDiscord(c *outbound.Client, s *ServiceSettings, sec *Secrets) (*discord, error) {
	u, err := requireURL(sec.WebhookURL, "Discord webhook URL")
	if err != nil {
		return nil, err
	}
	if len([]rune(s.Username)) > 80 {
		return nil, configErr("the Discord display name is limited to 80 characters")
	}
	return &discord{c: c, url: u, username: s.Username}, nil
}

type discordEmbed struct {
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	URL         string              `json:"url,omitempty"`
	Color       int                 `json:"color"`
	Timestamp   string              `json:"timestamp,omitempty"`
	Thumbnail   *discordURL         `json:"thumbnail,omitempty"`
	Fields      []discordEmbedField `json:"fields,omitempty"`
	Footer      *discordFooter      `json:"footer,omitempty"`
}

type discordURL struct {
	URL string `json:"url"`
}

type discordFooter struct {
	Text string `json:"text"`
}

type discordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type discordBody struct {
	Username        string         `json:"username,omitempty"`
	Embeds          []discordEmbed `json:"embeds"`
	AllowedMentions struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
}

// discordPayload builds the execute-webhook body: one embed, no mentions
// (release names may contain "@everyone").
func discordPayload(m *Message, username string) discordBody {
	e := discordEmbed{
		Title: truncate(m.Title, 256), Description: truncate(m.Body, 4096), URL: m.Link, Color: colorOf(m),
		Timestamp: m.Time.UTC().Format(time.RFC3339), Footer: &discordFooter{Text: "Gonema"},
	}
	if m.PosterURL != "" {
		e.Thumbnail = &discordURL{URL: m.PosterURL}
	}
	for i, f := range m.Fields {
		if i == 25 {
			break
		}
		e.Fields = append(e.Fields, discordEmbedField{
			Name: truncate(f.Label, 256), Value: truncate(f.Value, 1024), Inline: f.Key != "release" && f.Key != "reason",
		})
	}
	b := discordBody{Username: username, Embeds: []discordEmbed{e}}
	b.AllowedMentions.Parse = []string{}
	return b
}

func (d *discord) Test(ctx context.Context) error { return d.Send(ctx, TestMessage(time.Now())) }

func (d *discord) Send(ctx context.Context, m *Message) error {
	body, err := json.Marshal(discordPayload(m, d.username))
	if err != nil {
		return d.c.Fail(outbound.ErrResponse, 0, "cannot encode the message")
	}
	resp, err := d.c.Do(ctx, http.MethodPost, d.url, http.Header{"Content-Type": {"application/json"}}, body)
	if err != nil {
		return err
	}
	var e struct {
		Message    string  `json:"message"`
		RetryAfter float64 `json:"retry_after"`
	}
	_ = json.Unmarshal(resp.Body, &e) // best effort: details only
	err = d.c.Check(resp, e.Message)
	return withRetryAfter(err, e.RetryAfter)
}

// withRetryAfter adds a retry delay given in the body (seconds) to an error
// whose headers had none.
func withRetryAfter(err error, seconds float64) error {
	var oe *outbound.Error
	if err == nil || seconds <= 0 || !asOutbound(err, &oe) || oe.RetryAfter > 0 || oe.Status != http.StatusTooManyRequests {
		return err
	}
	oe.RetryAfter = min(time.Duration(seconds*float64(time.Second)), outbound.MaxRetryAfter)
	return oe
}
