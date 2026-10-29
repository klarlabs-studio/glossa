//go:build system

package m5_test

import (
	"encoding/base64"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// The SMTP account the server is configured with. The test mailer
// refuses a session that does not authenticate as it, so the suite
// proves the deployment's real path — GLOSSA_MAIL_DRIVER=smtp with
// credentials, the owner's decision of RFC 0006 §15 q1 — and not the
// `log` driver every earlier exit test signs in through.
const (
	smtpUser     = "glossa-m5"
	smtpPassword = "m5-smtp-password"
	mailFrom     = "Glossa <no-reply@m5.glossa.test>"
)

// mailbox is the test mailer: a capturing SMTP submission server on
// loopback. It speaks enough of RFC 5321 for net/smtp — EHLO, AUTH
// PLAIN, MAIL, RCPT, DATA — keeps every message it accepts, and never
// relays anything anywhere.
//
// It offers no STARTTLS, so the server runs with
// GLOSSA_SMTP_ALLOW_PLAINTEXT (the adapter's switch for a local relay or
// a test server); net/smtp still only sends AUTH over plaintext to
// loopback, which is where this is.
type mailbox struct {
	ln   net.Listener
	mu   sync.Mutex
	msgs []mailMessage
	// refused counts sessions that tried to send without
	// authenticating; the report says it is zero.
	refused int
}

// mailMessage is one accepted message, decoded.
type mailMessage struct {
	at      time.Time
	to      string
	subject string
	text    string
}

func startMailbox(t *testing.T) *mailbox {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &mailbox{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go m.serve()
	return m
}

func (m *mailbox) addr() string { return m.ln.Addr().String() }

func (m *mailbox) serve() {
	for {
		conn, err := m.ln.Accept()
		if err != nil {
			return
		}
		go m.session(conn)
	}
}

func (m *mailbox) session(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	tp := textproto.NewConn(conn)
	_ = tp.PrintfLine("220 m5 test mailer ESMTP")
	authed := false
	var rcpt string
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		fields := strings.Fields(line + " x")
		switch strings.ToUpper(fields[0]) {
		case "EHLO", "HELO":
			_ = tp.PrintfLine("250-m5\r\n250 AUTH PLAIN")
		case "AUTH":
			authed = m.auth(fields)
			if authed {
				_ = tp.PrintfLine("235 2.7.0 authenticated")
			} else {
				_ = tp.PrintfLine("535 5.7.8 bad credentials")
			}
		case "MAIL":
			if !authed {
				m.mu.Lock()
				m.refused++
				m.mu.Unlock()
				_ = tp.PrintfLine("530 5.7.0 authentication required")
				continue
			}
			_ = tp.PrintfLine("250 ok")
		case "RCPT":
			rcpt = strings.Trim(strings.TrimPrefix(strings.TrimPrefix(line, "RCPT TO:"), "rcpt to:"), "<> ")
			_ = tp.PrintfLine("250 ok")
		case "DATA":
			_ = tp.PrintfLine("354 go ahead")
			raw, err := io.ReadAll(tp.DotReader())
			if err != nil {
				return
			}
			m.keep(rcpt, raw)
			_ = tp.PrintfLine("250 queued")
		case "RSET", "NOOP":
			_ = tp.PrintfLine("250 ok")
		case "QUIT":
			_ = tp.PrintfLine("221 bye")
			return
		default:
			_ = tp.PrintfLine("502 not implemented")
		}
	}
}

// auth checks AUTH PLAIN's initial response against the configured
// account.
func (m *mailbox) auth(fields []string) bool {
	if len(fields) < 3 || !strings.EqualFold(fields[1], "PLAIN") {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(fields[2])
	if err != nil {
		return false
	}
	parts := strings.Split(string(raw), "\x00")
	return len(parts) == 3 && parts[1] == smtpUser && parts[2] == smtpPassword
}

func (m *mailbox) keep(rcpt string, raw []byte) {
	msg := mailMessage{at: time.Now(), to: strings.ToLower(rcpt)}
	if parsed, err := mail.ReadMessage(strings.NewReader(string(raw))); err == nil {
		msg.subject = parsed.Header.Get("Subject")
		body, _ := io.ReadAll(parsed.Body)
		if strings.EqualFold(parsed.Header.Get("Content-Transfer-Encoding"), "quoted-printable") {
			if dec, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(string(body)))); err == nil {
				body = dec
			}
		}
		msg.text = strings.ReplaceAll(string(body), "\r\n", "\n")
	} else {
		msg.text = string(raw)
	}
	m.mu.Lock()
	m.msgs = append(m.msgs, msg)
	m.mu.Unlock()
}

var mailedToken = regexp.MustCompile(`/auth/sign-in#token=([A-Za-z0-9_-]{43})`)

// linkFor waits for a sign-in link mailed to addr after since and
// returns its token. Registration's verification mail and the magic
// link are both sign-in links (identity/app/signin.go).
func (m *mailbox) linkFor(addr string, since time.Time) (string, error) {
	addr = strings.ToLower(addr)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		for i := len(m.msgs) - 1; i >= 0; i-- {
			msg := m.msgs[i]
			if msg.to != addr || msg.at.Before(since) {
				continue
			}
			if t := mailedToken.FindStringSubmatch(msg.text); t != nil {
				m.mu.Unlock()
				return t[1], nil
			}
		}
		m.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	return "", fmt.Errorf("the test mailer received no sign-in link for %s (it holds %d message(s), "+
		"%d unauthenticated attempt(s))", addr, m.count(), m.refusedCount())
}

func (m *mailbox) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.msgs)
}

func (m *mailbox) refusedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refused
}
