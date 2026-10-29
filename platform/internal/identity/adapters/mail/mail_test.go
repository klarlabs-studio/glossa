package mail_test

import (
	"context"
	"io"
	"log/slog"
	"mime/quotedprintable"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/mail"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
)

// fakeSMTP is a minimal SMTP server: enough of RFC 5321 for net/smtp.
type fakeSMTP struct {
	ln       net.Listener
	starttls bool
	mu       sync.Mutex
	rcpt     []string
	data     []string
}

func startFake(t *testing.T, starttls bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, starttls: starttls}
	t.Cleanup(func() { _ = ln.Close() })
	go f.serve()
	return f
}

func (f *fakeSMTP) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.session(conn)
	}
}

func (f *fakeSMTP) session(conn net.Conn) {
	defer conn.Close()
	tp := textproto.NewConn(conn)
	_ = tp.PrintfLine("220 fake ESMTP")
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb := strings.ToUpper(strings.Fields(line + " x")[0])
		switch verb {
		case "EHLO", "HELO":
			if f.starttls {
				_ = tp.PrintfLine("250-fake\r\n250 STARTTLS")
			} else {
				_ = tp.PrintfLine("250 fake")
			}
		case "MAIL":
			_ = tp.PrintfLine("250 ok")
		case "RCPT":
			f.mu.Lock()
			f.rcpt = append(f.rcpt, line)
			f.mu.Unlock()
			_ = tp.PrintfLine("250 ok")
		case "DATA":
			_ = tp.PrintfLine("354 go ahead")
			b, err := io.ReadAll(tp.DotReader())
			if err != nil {
				return
			}
			f.mu.Lock()
			f.data = append(f.data, string(b))
			f.mu.Unlock()
			_ = tp.PrintfLine("250 queued")
		case "QUIT":
			_ = tp.PrintfLine("221 bye")
			return
		default:
			_ = tp.PrintfLine("502 not implemented")
		}
	}
}

func TestSMTPDeliversAPlainTextMessage(t *testing.T) {
	f := startFake(t, false)
	m, err := mail.NewSMTP(mail.SMTPConfig{
		Addr: f.ln.Addr().String(), From: "Glossa <no-reply@glossa.test>",
		Timeout: 5 * time.Second, AllowPlaintext: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	link := "https://studio.test/auth/sign-in#token=" + strings.Repeat("A", 43)
	err = m.Send(context.Background(), app.Message{
		To: "ada@example.com", Subject: "Sign in to Glossa — ✓", Text: "Hello\n\n" + link + "\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.data) != 1 || len(f.rcpt) != 1 || !strings.Contains(f.rcpt[0], "<ada@example.com>") {
		t.Fatalf("rcpt = %v, messages = %d", f.rcpt, len(f.data))
	}
	msg := f.data[0]
	for _, want := range []string{
		"From: \"Glossa\" <no-reply@glossa.test>", "To: <ada@example.com>",
		"Subject: =?utf-8?q?", "Content-Transfer-Encoding: quoted-printable", "Message-ID: <",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	_, body, _ := strings.Cut(msg, "\n\n") // DotReader normalizes CRLF
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
	if err != nil || !strings.Contains(string(decoded), link) {
		t.Errorf("body doesn't carry the link intact: %q, %v", decoded, err)
	}
}

func TestSMTPRefusesPlaintextServersByDefault(t *testing.T) {
	f := startFake(t, false)
	m, err := mail.NewSMTP(mail.SMTPConfig{Addr: f.ln.Addr().String(), From: "no-reply@glossa.test", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	err = m.Send(context.Background(), app.Message{To: "ada@example.com", Subject: "x", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("err = %v, want a STARTTLS refusal", err)
	}
}

func TestSMTPRejectsHeaderInjection(t *testing.T) {
	m, err := mail.NewSMTP(mail.SMTPConfig{Addr: "127.0.0.1:1", From: "no-reply@glossa.test", AllowPlaintext: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range []app.Message{
		{To: "ada@example.com\r\nBcc: eve@example.com", Subject: "x"},
		{To: "ada@example.com", Subject: "x\r\nBcc: eve@example.com"},
	} {
		if err := m.Send(context.Background(), msg); err == nil {
			t.Errorf("message %+v was accepted", msg)
		}
	}
}

func TestNewSMTPValidatesConfig(t *testing.T) {
	if _, err := mail.NewSMTP(mail.SMTPConfig{Addr: "no-port", From: "a@b.test"}); err == nil {
		t.Error("address without port accepted")
	}
	if _, err := mail.NewSMTP(mail.SMTPConfig{Addr: "smtp.test:587", From: "not an address"}); err == nil {
		t.Error("bad sender accepted")
	}
}

func TestLogMailerLogsTheMessage(t *testing.T) {
	var buf strings.Builder
	l := mail.Log{Logger: slog.New(slog.NewTextHandler(&buf, nil))}
	if err := l.Send(context.Background(), app.Message{To: "ada@example.com", Subject: "Hi", Text: "link"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "ada@example.com") || !strings.Contains(buf.String(), "link") {
		t.Errorf("log = %s", buf.String())
	}
}
