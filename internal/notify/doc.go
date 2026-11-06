// Package notify sends notifications about what Gonema does: grabs, imports,
// upgrades, failures, health issues, renames and removals.
//
// # Flow
//
// The Service follows the event bus with a buffered subscription (the bus
// never waits for it). For each event it builds one Message (title, plain
// body, one-line summary, poster URL, link, fields) and queues it for every
// enabled notification whose events include its kind and whose tags meet the
// movie's tags (no tags: every movie; health messages ignore tags). A bounded
// pool of workers delivers the queue; a full queue drops the message (logged,
// counted) rather than slowing anything down. Deliveries are retried on 429,
// 5xx and network errors, after the longer of the backoff step and the
// server's Retry-After. Each outcome is recorded on the notification
// (failure_count, last_error); FailuresBeforeHealth failed deliveries in a row
// raise a health issue (health.changed, source "notification"), and the next
// success publishes the recovery. A notification never receives the health
// messages about itself.
//
// Messages follow the voice of DESIGN.md: calm, short, what happened and what
// Gonema does next, no emoji. Examples:
//
//	Grabbed — Halcyon Drift (2024) · Remux-2160p · 58.4 GB from Nebula NZB
//	Upgraded — Halcyon Drift (2024): WEBDL-2160p → Remux-2160p
//	Download failed — Halcyon Drift (2024). Blocklisted it, trying release #2 of 6.
//
// # Generic webhook, schema gonema.v1
//
// POST, Content-Type application/json, body Payload:
//
//	{
//	  "schema": "gonema.v1",
//	  "event": "grab|import|upgrade|failure|health|rename|delete|test",
//	  "time": "2026-11-05T10:00:00Z",
//	  "title": "...", "body": "...", "summary": "...", "problem": false,
//	  "movie": {"id": 1, "tmdbId": 2, "title": "...", "year": 2024, "posterUrl": "...", "link": "..."} | null,
//	  "fields": [{"key": "quality", "label": "Quality", "value": "Remux-2160p"}, ...],
//	  "data": {"quality": "Remux-2160p", ...},
//	  "health": {"source": "indexer", "id": 3}            (health messages only)
//	}
//
// Field keys: quality, previousQuality, size, indexer, client, release, reason.
// Within gonema.v1 fields are only added, never renamed or removed.
//
// Headers: X-Gonema-Event (the event), X-Gonema-Delivery (random id, to
// deduplicate retries), X-Gonema-Timestamp (Unix seconds) and, when a signing
// secret is set, X-Gonema-Signature: "sha256=" + hex(HMAC-SHA256(secret,
// timestamp + "." + body)). Receivers should compare in constant time and
// refuse old timestamps. Custom headers (secret values) may be added; they
// cannot replace these nor Content-Type, Host or User-Agent.
//
// # References
//
// Checked against the official documentation on 2026-11-05:
//
//   - Discord, https://discord.com/developers/docs/resources/webhook — Execute
//     Webhook: JSON with content/embeds (up to 10), username, allowed_mentions;
//     204 without ?wait; 429 carries retry_after (seconds) and Retry-After.
//   - Telegram Bot API, https://core.telegram.org/bots/api — sendMessage
//     (chat_id, message_thread_id, text up to 4096, parse_mode "HTML",
//     disable_notification, link_preview_options); errors carry description
//     and parameters.retry_after. The token is part of the URL path
//     (/bot<token>/sendMessage): it is redacted from every error.
//   - Pushover, https://pushover.net/api — POST /1/messages.json with token,
//     user (30 characters each), message (1024), title (250), url (512),
//     url_title (100), priority, device, sound, timestamp; 429 when the monthly
//     limit is reached.
//   - ntfy, https://docs.ntfy.sh/publish/ — "Publish as JSON": POST to the
//     server root with topic, title, message, tags, priority (1–5), click,
//     icon; Authorization: Bearer <token> or Basic.
//   - Gotify, https://gotify.net/docs/pushmsg and https://gotify.net/docs/msgextras —
//     POST /message with the X-Gotify-Key header, JSON title, message,
//     priority, extras client::notification.click.url and bigImageUrl.
//   - Slack, https://api.slack.com/messaging/webhooks — incoming webhook: JSON
//     text (fallback) and blocks; mrkdwn escapes &, < and >.
//   - Email: RFC 5321 through net/smtp, STARTTLS (587) or implicit TLS (465);
//     the password is only sent over TLS (net/smtp PlainAuth), never logged.
//
// # Security
//
// URLs are http or https with a host and no user info; redirects are never
// followed; responses are capped; secrets (webhook URLs, tokens, keys,
// passwords, header values) are encrypted at rest (secret box, bound to
// "notifications/<id>"), never returned by the API and redacted from errors.
package notify
