package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/fs0ciety7000/gonema/internal/outbound"
)

// SMTP encryption modes.
const (
	EncryptionSTARTTLS = "starttls"
	EncryptionTLS      = "tls"
	EncryptionNone     = "none"
)

const emailTimeout = 30 * time.Second

type email struct {
	host string
	port int
	enc  string
	from *mail.Address
	to   []*mail.Address
	user string
	pass string
	red  *outbound.Redactor
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	now  func() time.Time
	// tlsConfig is used for both STARTTLS and implicit TLS; tests set RootCAs.
	tlsConfig *tls.Config
}

func newEmail(s *ServiceSettings, sec *Secrets, red *outbound.Redactor, opts Options) (*email, error) {
	host := strings.TrimSpace(s.Host)
	if host == "" || strings.ContainsAny(host, "/:@ \t") && net.ParseIP(host) == nil {
		return nil, configErr("the SMTP host is required (a host name or IP address, without scheme or port)")
	}
	enc := s.Encryption
	if enc == "" {
		enc = EncryptionSTARTTLS
	}
	port := s.Port
	switch enc {
	case EncryptionSTARTTLS:
		port = cmpOr(port, 587)
	case EncryptionTLS:
		port = cmpOr(port, 465)
	case EncryptionNone:
		port = cmpOr(port, 25)
	default:
		return nil, configErr("the SMTP encryption must be starttls, tls or none")
	}
	if port < 1 || port > 65535 {
		return nil, configErr("the SMTP port must be between 1 and 65535")
	}
	from, err := mail.ParseAddress(strings.TrimSpace(s.From))
	if err != nil {
		return nil, configErr("the sender address is not valid")
	}
	if from.Name == "" {
		from.Name = "Gonema"
	}
	if len(s.To) == 0 || len(s.To) > 20 {
		return nil, configErr("give 1 to 20 recipient addresses")
	}
	to := make([]*mail.Address, 0, len(s.To))
	for _, raw := range s.To {
		a, err := mail.ParseAddress(strings.TrimSpace(raw))
		if err != nil {
			return nil, configErr("the recipient %q is not a valid address", truncate(raw, 80))
		}
		to = append(to, a)
	}
	dial := opts.Dial
	if dial == nil {
		// TODO(phase8): netx — dial through the SSRF-hardened dialer.
		d := &net.Dialer{Timeout: 10 * time.Second}
		dial = d.DialContext
	}
	return &email{
		host: host, port: port, enc: enc, from: from, to: to, user: strings.TrimSpace(s.Username), pass: sec.Password,
		red: red, dial: dial, now: opts.Now, tlsConfig: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
	}, nil
}

func cmpOr(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// smtpError is a failed SMTP exchange; its message is redacted.
type smtpError struct {
	msg       string
	temporary bool
}

func (e *smtpError) Error() string   { return "Email: " + e.msg }
func (e *smtpError) Temporary() bool { return e.temporary }

// fail wraps an SMTP or network error: 4xx replies and network errors are
// temporary, 5xx replies final. The password never appears (net/smtp does not
// echo it; the redactor is a second line).
func (e *email) fail(step string, err error) error {
	var tp *textproto.Error
	temporary := true
	detail := ""
	switch {
	case errors.As(err, &tp):
		temporary = tp.Code >= 400 && tp.Code < 500
		detail = fmt.Sprintf("the server answered %d %s", tp.Code, tp.Msg)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		detail = "no answer in time"
		temporary = !errors.Is(err, context.Canceled)
	default:
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			detail = "no answer in time"
		} else {
			detail = err.Error()
		}
	}
	return &smtpError{msg: e.red.Redact(step + ": " + truncate(detail, 300)), temporary: temporary}
}

func (e *email) Test(ctx context.Context) error { return e.Send(ctx, TestMessage(e.now())) }

func (e *email) Send(ctx context.Context, m *Message) error {
	ctx, cancel := context.WithTimeout(ctx, emailTimeout)
	defer cancel()
	addr := net.JoinHostPort(e.host, strconv.Itoa(e.port))
	conn, err := e.dial(ctx, "tcp", addr)
	if err != nil {
		return e.fail("cannot connect to "+addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	if e.enc == EncryptionTLS {
		tc := tls.Client(conn, e.tlsConfig)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return e.fail("secure connection failed (is port "+strconv.Itoa(e.port)+" implicit TLS?)", err)
		}
		conn = tc
	}
	c, err := smtp.NewClient(conn, e.host)
	if err != nil {
		_ = conn.Close()
		return e.fail("greeting", err)
	}
	defer c.Close()
	if e.enc == EncryptionSTARTTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return &smtpError{msg: "the server does not offer STARTTLS: choose tls (port 465) or, on a trusted network, none"}
		}
		if err := c.StartTLS(e.tlsConfig); err != nil {
			return e.fail("STARTTLS", err)
		}
	}
	if e.user != "" {
		// PlainAuth refuses to send the password over an unencrypted
		// connection, except to localhost.
		if err := c.Auth(smtp.PlainAuth("", e.user, e.pass, e.host)); err != nil {
			if strings.Contains(err.Error(), "unencrypted connection") {
				return &smtpError{msg: "the password is not sent over an unencrypted connection: choose starttls or tls"}
			}
			return e.fail("login refused", err)
		}
	}
	if err := c.Mail(e.from.Address); err != nil {
		return e.fail("sender refused", err)
	}
	for _, a := range e.to {
		if err := c.Rcpt(a.Address); err != nil {
			return e.fail("recipient refused", err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return e.fail("DATA", err)
	}
	msg, err := e.compose(m)
	if err != nil {
		_ = w.Close()
		return &smtpError{msg: "cannot build the message"}
	}
	if _, err := w.Write(msg); err != nil {
		return e.fail("sending the message", err)
	}
	if err := w.Close(); err != nil {
		return e.fail("message refused", err)
	}
	if err := c.Quit(); err != nil {
		return e.fail("QUIT", err)
	}
	return nil
}

// oneLine removes line breaks from a header value.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// compose builds a multipart/alternative message: plain text and simple HTML.
func (e *email) compose(m *Message) ([]byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	to := make([]string, 0, len(e.to))
	for _, a := range e.to {
		to = append(to, a.String())
	}
	var id [12]byte
	_, _ = rand.Read(id[:])
	hdr := []string{
		"From: " + e.from.String(),
		"To: " + strings.Join(to, ", "),
		"Subject: " + mime.QEncoding.Encode("utf-8", oneLine(m.Title)),
		"Date: " + e.now().UTC().Format(time.RFC1123Z),
		"Message-ID: <" + hex.EncodeToString(id[:]) + "@gonema>",
		"MIME-Version: 1.0",
		"Content-Type: multipart/alternative; boundary=\"" + mw.Boundary() + "\"",
	}
	var out bytes.Buffer // headers, then the parts written to buf
	out.WriteString(strings.Join(hdr, "\r\n") + "\r\n\r\n")

	for _, part := range []struct{ ctype, body string }{
		{"text/plain; charset=utf-8", m.Text()},
		{"text/html; charset=utf-8", emailHTML(m)},
	} {
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type": {part.ctype}, "Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		qw := quotedprintable.NewWriter(pw)
		if _, err := qw.Write([]byte(part.body)); err != nil {
			return nil, err
		}
		if err := qw.Close(); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	out.Write(buf.Bytes())
	return out.Bytes(), nil
}

// emailHTML is a minimal, escaped HTML rendering of m.
func emailHTML(m *Message) string {
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!doctype html><html><body style="font-family:sans-serif;line-height:1.5">`)
	if m.PosterURL != "" {
		b.WriteString(`<img src="` + esc(m.PosterURL) + `" alt="` + esc(m.Movie.Display()) + ` poster" width="120" style="float:right;margin-left:16px">`)
	}
	b.WriteString("<h2>" + esc(m.Title) + "</h2>")
	if m.Body != "" {
		b.WriteString("<p>" + esc(m.Body) + "</p>")
	}
	if len(m.Fields) > 0 {
		b.WriteString("<table>")
		for _, f := range m.Fields {
			b.WriteString(`<tr><th align="left" style="padding-right:12px">` + esc(f.Label) + "</th><td>" + esc(f.Value) + "</td></tr>")
		}
		b.WriteString("</table>")
	}
	if m.Link != "" {
		b.WriteString(`<p><a href="` + esc(m.Link) + `">Open in Gonema</a></p>`)
	}
	b.WriteString("</body></html>")
	return b.String()
}
