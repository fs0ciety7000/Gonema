package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/outbound"
)

type slack struct {
	c   *outbound.Client
	url string
}

func newSlack(c *outbound.Client, _ *ServiceSettings, sec *Secrets) (*slack, error) {
	u, err := requireURL(sec.WebhookURL, "Slack webhook URL")
	if err != nil {
		return nil, err
	}
	return &slack{c: c, url: u}, nil
}

// slackEscape escapes the three characters mrkdwn reserves.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// slackPayload builds an incoming-webhook body: text is the notification
// fallback; blocks hold a header, the body (with the poster as accessory),
// the fields and the link.
func slackPayload(m *Message) map[string]any {
	section := map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": truncate(slackEscape(m.Body), 3000)}}
	if m.Body == "" {
		section["text"] = map[string]any{"type": "mrkdwn", "text": " "}
	}
	if m.PosterURL != "" {
		section["accessory"] = map[string]any{"type": "image", "image_url": m.PosterURL, "alt_text": truncate(m.Movie.Display()+" poster", 2000)}
	}
	blocks := []any{
		map[string]any{"type": "header", "text": map[string]any{"type": "plain_text", "text": truncate(m.Title, 150), "emoji": false}},
		section,
	}
	if len(m.Fields) > 0 {
		var fields []any
		for i, f := range m.Fields {
			if i == 10 { // section fields limit
				break
			}
			fields = append(fields, map[string]any{"type": "mrkdwn", "text": truncate("*"+slackEscape(f.Label)+"*\n"+slackEscape(f.Value), 2000)})
		}
		blocks = append(blocks, map[string]any{"type": "section", "fields": fields})
	}
	if m.Link != "" && !strings.ContainsAny(m.Link, "|<>") {
		blocks = append(blocks, map[string]any{"type": "context", "elements": []any{
			map[string]any{"type": "mrkdwn", "text": "<" + m.Link + "|Open in Gonema>"},
		}})
	}
	return map[string]any{"text": truncate(m.Summary, 3000), "blocks": blocks}
}

func (s *slack) Test(ctx context.Context) error { return s.Send(ctx, TestMessage(time.Now())) }

func (s *slack) Send(ctx context.Context, m *Message) error {
	body, err := json.Marshal(slackPayload(m))
	if err != nil {
		return s.c.Fail(outbound.ErrResponse, 0, "cannot encode the message")
	}
	resp, err := s.c.Do(ctx, http.MethodPost, s.url, http.Header{"Content-Type": {"application/json"}}, body)
	if err != nil {
		return err
	}
	return s.c.Check(resp, snippet(resp.Body)) // errors are short codes: invalid_payload, no_service...
}
