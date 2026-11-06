package notify

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal SMTP server (RFC 5321 subset): EHLO, AUTH PLAIN,
// MAIL, RCPT, DATA, QUIT. No STARTTLS: the tests use the "none" mode on
// 127.0.0.1, where net/smtp allows PLAIN auth.
type fakeSMTP struct {
	ln       net.Listener
	password string // accepted AUTH PLAIN password; "" refuses AUTH
	echoAuth bool   // the 535 reply quotes the credentials (hostile server)
	rcptCode int    // reply to RCPT, default 250

	mu       sync.Mutex
	from     string
	rcpts    []string
	data     string
	authUser string
	wg       sync.WaitGroup
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, rcptCode: 250}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.wg.Add(1)
			go func() {
				defer f.wg.Done()
				f.serve(c)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		f.wg.Wait()
	})
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	tp := textproto.NewConn(c)
	reply := func(code int, msg string) { _ = tp.PrintfLine("%d %s", code, msg) }
	reply(220, "fake.smtp ESMTP ready")
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			_ = tp.PrintfLine("250-fake.smtp greets you")
			_ = tp.PrintfLine("250-8BITMIME")
			_ = tp.PrintfLine("250 AUTH PLAIN")
		case "AUTH":
			parts := strings.Fields(arg)
			raw, _ := base64.StdEncoding.DecodeString(parts[len(parts)-1])
			fields := strings.Split(string(raw), "\x00")
			if len(fields) == 3 && f.password != "" && fields[2] == f.password {
				f.mu.Lock()
				f.authUser = fields[1]
				f.mu.Unlock()
				reply(235, "2.7.0 Authentication successful")
				continue
			}
			msg := "5.7.8 Authentication credentials invalid"
			if f.echoAuth && len(fields) == 3 {
				msg += " for " + fields[1] + ":" + fields[2]
			}
			reply(535, msg)
		case "MAIL":
			f.mu.Lock()
			f.from = arg
			f.mu.Unlock()
			reply(250, "2.1.0 OK")
		case "RCPT":
			if f.rcptCode != 250 {
				reply(f.rcptCode, "4.2.2 Mailbox full, try later")
				continue
			}
			f.mu.Lock()
			f.rcpts = append(f.rcpts, arg)
			f.mu.Unlock()
			reply(250, "2.1.5 OK")
		case "DATA":
			reply(354, "End data with <CR><LF>.<CR><LF>")
			b, err := io.ReadAll(tp.DotReader())
			if err != nil {
				return
			}
			f.mu.Lock()
			f.data = string(b)
			f.mu.Unlock()
			reply(250, "2.0.0 Queued")
		case "QUIT":
			reply(221, "2.0.0 Bye")
			return
		case "RSET", "NOOP":
			reply(250, "OK")
		default:
			reply(502, "5.5.2 Command not recognized")
		}
	}
}

func emailConfig(f *fakeSMTP) Config {
	return Config{Kind: Email, Settings: ServiceSettings{
		Host: "127.0.0.1", Port: f.port(), Encryption: EncryptionNone,
		From: "Gonema <gonema@example.lan>", To: []string{"me@example.lan", "Other <other@example.lan>"},
	}}
}

func TestEmailPlain(t *testing.T) {
	f := newFakeSMTP(t)
	n := newNotifier(t, emailConfig(f))
	m := sampleMessage()
	m.Title = "Upgraded — Halcyon Drift (2024)\r\nBcc: evil@example.org"
	if err := n.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	from, rcpts, data := f.from, f.rcpts, f.data
	f.mu.Unlock()
	if !strings.HasPrefix(from, "FROM:<gonema@example.lan>") || len(rcpts) != 2 || rcpts[1] != "TO:<other@example.lan>" {
		t.Errorf("envelope: %q %q", from, rcpts)
	}
	msg, err := mail.ReadMessage(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("Bcc") != "" {
		t.Error("header injection through the subject")
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != "Upgraded — Halcyon Drift (2024) Bcc: evil@example.org" {
		t.Errorf("subject = %q, %v", subject, err)
	}
	if msg.Header.Get("Message-ID") == "" || msg.Header.Get("Date") == "" || !strings.Contains(msg.Header.Get("To"), "other@example.lan") {
		t.Errorf("headers = %v", msg.Header)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("content type %q: %v", mt, err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var parts []string
	for {
		p, err := mr.NextPart() // decodes quoted-printable
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		parts = append(parts, p.Header.Get("Content-Type")+"\n"+string(b))
	}
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "text/plain") || !strings.HasPrefix(parts[1], "text/html") {
		t.Fatalf("parts = %q", parts)
	}
	if !strings.Contains(parts[0], "Quality: Remux-2160p") || !strings.Contains(parts[0], m.Link) {
		t.Errorf("plain part = %q", parts[0])
	}
	if !strings.Contains(parts[1], "&lt;@everyone&gt; &amp; co") || strings.Contains(parts[1], "<@everyone>") || !strings.Contains(parts[1], `href="`+m.Link+`"`) {
		t.Errorf("html part = %q", parts[1])
	}
}

func TestEmailAuthAndErrors(t *testing.T) {
	const password = "smtp-Pa55word-xyz"
	f := newFakeSMTP(t)
	f.password, f.echoAuth = "something-else", true
	cfg := emailConfig(f)
	cfg.Settings.Username = "gonema"
	cfg.Secrets.Password = password
	err := newNotifier(t, cfg).Send(context.Background(), sampleMessage())
	var se *smtpError
	if !errors.As(err, &se) || se.Temporary() || !strings.Contains(err.Error(), "535") {
		t.Fatalf("bad password: %v", err)
	}
	if strings.Contains(err.Error(), password) {
		t.Errorf("the password leaks into the error: %v", err)
	}

	f.password = password
	if err := newNotifier(t, cfg).Send(context.Background(), sampleMessage()); err != nil {
		t.Fatalf("auth: %v", err)
	}
	f.mu.Lock()
	if f.authUser != "gonema" {
		t.Errorf("auth user = %q", f.authUser)
	}
	f.mu.Unlock()

	f.rcptCode = 452
	err = newNotifier(t, cfg).Send(context.Background(), sampleMessage())
	if !errors.As(err, &se) || !se.Temporary() || !strings.Contains(err.Error(), "recipient refused") {
		t.Errorf("4xx must be temporary: %v", err)
	}

	// No password over an unencrypted connection to another host.
	remote := emailConfig(f)
	remote.Settings.Host = "smtp.example.org"
	remote.Settings.Username = "gonema"
	remote.Secrets.Password = password
	n, err := New(remote, Options{Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, "127.0.0.1:"+strconv.Itoa(f.port()))
	}})
	if err != nil {
		t.Fatal(err)
	}
	err = n.Send(context.Background(), sampleMessage())
	if err == nil || !strings.Contains(err.Error(), "unencrypted") || strings.Contains(err.Error(), password) {
		t.Errorf("plain auth to a remote host: %v", err)
	}

	// STARTTLS required but not offered.
	tlsCfg := emailConfig(f)
	tlsCfg.Settings.Encryption = EncryptionSTARTTLS
	if err := newNotifier(t, tlsCfg).Send(context.Background(), sampleMessage()); err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("starttls: %v", err)
	}

	// Nothing listening.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	down := emailConfig(f)
	down.Settings.Port = port
	err = newNotifier(t, down).Send(context.Background(), sampleMessage())
	if !errors.As(err, &se) || !se.Temporary() {
		t.Errorf("refused: %v", err)
	}
}
