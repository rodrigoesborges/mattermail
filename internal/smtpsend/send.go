// Package smtpsend sends raw emails over SMTP.
package smtpsend

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"log/slog"

	"github.com/rodrigoesborges/mattermail/internal/config"
)

// Sender delivers email through one SMTP account.
type Sender struct {
	cfg config.SMTP
	log *slog.Logger
}

// New creates an SMTP sender.
func New(cfg config.SMTP, log *slog.Logger) *Sender {
	return &Sender{cfg: cfg, log: log}
}

// Send delivers a raw message to the given recipients.
func (s *Sender) Send(ctx context.Context, raw []byte, to []string) error {
	done := make(chan error, 1)
	go func() {
		done <- s.send(raw, to)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Sender) send(raw []byte, to []string) error {
	c, err := s.dial()
	if err != nil {
		return err
	}
	defer c.Close() //nolint:errcheck // best effort; Quit below is the clean path

	if s.cfg.Username != "" {
		auth := sasl.NewPlainClient("", s.cfg.Username, s.cfg.Password)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(s.cfg.From, nil); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt, nil); err != nil {
			return fmt.Errorf("smtp RCPT TO %s: %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		w.Close()
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close body: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp QUIT: %w", err)
	}
	return nil
}

func (s *Sender) dial() (*smtp.Client, error) {
	tlsCfg := &tls.Config{
		ServerName:         hostOf(s.cfg.Server),
		InsecureSkipVerify: s.cfg.InsecureSkipVerify, //nolint:gosec // opt-in
	}

	switch s.cfg.TLS {
	case config.TLSSSL:
		c, err := smtp.DialTLS(s.cfg.Server, tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("dial tls %s: %w", s.cfg.Server, err)
		}
		return c, nil
	case config.TLSStartTLS:
		c, err := smtp.DialStartTLS(s.cfg.Server, tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("dial starttls %s: %w", s.cfg.Server, err)
		}
		return c, nil
	default:
		c, err := smtp.Dial(s.cfg.Server)
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", s.cfg.Server, err)
		}
		return c, nil
	}
}

func hostOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
}
