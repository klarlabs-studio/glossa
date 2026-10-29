// Package mail implements Identity's Mailer port: Log for development
// and SMTP for everything else. A Resend (HTTP API) adapter can join them
// later behind the same port.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"go.klarlabs.de/fortify/retry"

	"go.klarlabs.de/glossa/platform/internal/identity/app"
)

// Log writes mail to the log instead of sending it — links included, so
// a developer can click them. Never use it in production.
type Log struct{ Logger *slog.Logger }

// Send implements app.Mailer.
func (l Log) Send(ctx context.Context, m app.Message) error {
	l.Logger.InfoContext(ctx, "mail not sent (GLOSSA_MAIL_DRIVER=log)",
		slog.String("to", m.To), slog.String("subject", m.Subject), slog.String("text", m.Text))
	return nil
}

// SMTPConfig configures the SMTP adapter.
type SMTPConfig struct {
	// Addr is host:port of the submission server (usually port 587).
	Addr string
	// Username and Password enable SMTP AUTH PLAIN; empty disables it.
	Username, Password string
	// From is the sender, e.g. "Glossa <no-reply@glossa.dev>".
	From string
	// Timeout bounds one delivery attempt.
	Timeout time.Duration
	// AllowPlaintext permits a server without STARTTLS. Only for a local
	// relay or a test server; AUTH is still refused over plaintext except
	// to localhost (net/smtp).
	AllowPlaintext bool
}

// SMTP delivers mail through an SMTP submission server, retrying
// transient failures (fortify).
type SMTP struct {
	cfg   SMTPConfig
	host  string
	from  *mail.Address
	retry retry.Retry[struct{}]
}

var errPermanent = errors.New("mail: permanent failure")

// NewSMTP validates cfg and returns the adapter.
func NewSMTP(cfg SMTPConfig) (*SMTP, error) {
	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("mail: SMTP address %q: %w", cfg.Addr, err)
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("mail: sender %q: %w", cfg.From, err)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &SMTP{
		cfg:  cfg,
		host: host,
		from: from,
		retry: retry.New[struct{}](retry.Config{
			MaxAttempts:   3,
			InitialDelay:  200 * time.Millisecond,
			Multiplier:    2,
			BackoffPolicy: retry.BackoffExponential,
			Jitter:        true,
			IsRetryable:   func(err error) bool { return !errors.Is(err, errPermanent) },
		}),
	}, nil
}

// Send implements app.Mailer.
func (s *SMTP) Send(ctx context.Context, m app.Message) error {
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("mail: recipient %q: %w", m.To, err)
	}
	msg, err := s.compose(to, m)
	if err != nil {
		return err
	}
	_, err = s.retry.Execute(ctx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, s.deliver(ctx, to.Address, msg)
	})
	return err
}

// compose renders an RFC 5322 text/plain message.
func (s *SMTP) compose(to *mail.Address, m app.Message) ([]byte, error) {
	if strings.ContainsAny(m.Subject, "\r\n") {
		return nil, errors.New("mail: subject contains a line break")
	}
	var buf bytes.Buffer
	header := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	header("From", s.from.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", time.Now().UTC().Format(time.RFC1123Z))
	header("Message-ID", "<"+messageID()+"@"+domainOf(s.from.Address)+">")
	header("MIME-Version", "1.0")
	header("Content-Type", `text/plain; charset="utf-8"`)
	header("Content-Transfer-Encoding", "quoted-printable")
	buf.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&buf)
	if _, err := qp.Write([]byte(strings.ReplaceAll(m.Text, "\n", "\r\n"))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *SMTP) deliver(ctx context.Context, to string, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("mail: dial %s: %w", s.cfg.Addr, err)
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mail: greeting: %w", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Hello(domainOf(s.from.Address)); err != nil {
		return fmt.Errorf("mail: EHLO: %w", err)
	}
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mail: STARTTLS: %w", err)
		}
	} else if !s.cfg.AllowPlaintext {
		return fmt.Errorf("%w: %s offers no STARTTLS", errPermanent, s.cfg.Addr)
	}
	if s.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.host)); err != nil {
			return fmt.Errorf("%w: AUTH: %v", errPermanent, err)
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return fmt.Errorf("mail: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("mail: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("mail: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: end of data: %w", err)
	}
	return c.Quit()
}

func messageID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func domainOf(addr string) string {
	if _, d, ok := strings.Cut(addr, "@"); ok {
		return d
	}
	return "localhost"
}
