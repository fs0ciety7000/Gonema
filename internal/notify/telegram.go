package notify

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/outbound"
)

// DefaultTelegramAPI is the Bot API address.
const DefaultTelegramAPI = "https://api.telegram.org"

// botToken matches the shape of a bot token ("123456:ABC-DEF...").
var botToken = regexp.MustCompile(`^[0-9]{1,20}:[A-Za-z0-9_-]{20,100}$`)

type telegram struct {
	c        *outbound.Client
	endpoint string // contains the token: never logged, redacted from errors
	chatID   string
	threadID int64
	silent   bool
}

func newTelegram(c *outbound.Client, s *ServiceSettings, sec *Secrets) (*telegram, error) {
	if !botToken.MatchString(sec.Token) {
		return nil, configErr("the Telegram bot token is missing or malformed (it looks like 123456:ABC-DEF…, from @BotFather)")
	}
	chat := strings.TrimSpace(s.ChatID)
	if chat == "" || strings.ContainsAny(chat, " \t\r\n") {
		return nil, configErr("the Telegram chat id is required (a number, or @channelname)")
	}
	api := DefaultTelegramAPI
	if s.ServerURL != "" {
		var err error
		if api, err = requireURL(s.ServerURL, "Telegram API URL"); err != nil {
			return nil, err
		}
	}
	return &telegram{
		c: c, endpoint: strings.TrimRight(api, "/") + "/bot" + sec.Token + "/sendMessage",
		chatID: chat, threadID: s.ThreadID, silent: s.Silent,
	}, nil
}

type telegramBody struct {
	ChatID              string `json:"chat_id"`
	MessageThreadID     int64  `json:"message_thread_id,omitempty"`
	Text                string `json:"text"`
	ParseMode           string `json:"parse_mode"`
	DisableNotification bool   `json:"disable_notification,omitempty"`
	LinkPreviewOptions  struct {
		IsDisabled bool `json:"is_disabled"`
	} `json:"link_preview_options"`
}

// telegramText renders m in the HTML parse mode: only <b>, <a> and escaped
// text, which is all the Bot API needs to accept it.
func telegramText(m *Message) string {
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString("<b>" + esc(m.Title) + "</b>")
	if m.Body != "" {
		b.WriteString("\n" + esc(m.Body))
	}
	if len(m.Fields) > 0 {
		b.WriteString("\n")
		for _, f := range m.Fields {
			b.WriteString("\n" + esc(f.Label) + ": " + esc(f.Value))
		}
	}
	if m.Link != "" {
		b.WriteString("\n\n<a href=\"" + esc(m.Link) + "\">Open in Gonema</a>")
	}
	if len([]rune(b.String())) > 4096 { // Bot API limit: fall back to the escaped plain text
		return esc(truncate(m.Text(), 3500))
	}
	return b.String()
}

func telegramPayload(m *Message, chatID string, threadID int64, silent bool) telegramBody {
	b := telegramBody{ChatID: chatID, MessageThreadID: threadID, Text: telegramText(m), ParseMode: "HTML", DisableNotification: silent}
	b.LinkPreviewOptions.IsDisabled = true
	return b
}

func (t *telegram) Test(ctx context.Context) error { return t.Send(ctx, TestMessage(time.Now())) }

func (t *telegram) Send(ctx context.Context, m *Message) error {
	body, err := json.Marshal(telegramPayload(m, t.chatID, t.threadID, t.silent && !m.Problem))
	if err != nil {
		return t.c.Fail(outbound.ErrResponse, 0, "cannot encode the message")
	}
	resp, err := t.c.Do(ctx, http.MethodPost, t.endpoint, http.Header{"Content-Type": {"application/json"}}, body)
	if err != nil {
		return err
	}
	var r struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Parameters  struct {
			RetryAfter float64 `json:"retry_after"`
		} `json:"parameters"`
	}
	if jerr := json.Unmarshal(resp.Body, &r); jerr != nil && resp.Status < 300 {
		return t.c.Fail(outbound.ErrResponse, resp.Status, "the answer is not JSON")
	}
	if err := t.c.Check(resp, r.Description); err != nil {
		return withRetryAfter(err, r.Parameters.RetryAfter)
	}
	if !r.OK {
		return t.c.Fail(outbound.ErrStatus, resp.Status, r.Description)
	}
	return nil
}
