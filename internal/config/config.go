// Package config loads and validates the mattermail TOML configuration.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// TLS modes shared by [imap].tls and [smtp].tls.
const (
	TLSSSL      = "ssl"
	TLSStartTLS = "starttls"
	TLSNone     = "none"
)

type Config struct {
	Log    Log      `toml:"log"`
	IMAP   IMAP     `toml:"imap"`
	SMTP   SMTP     `toml:"smtp"`
	Bridge Bridge   `toml:"matterbridge"`
	MailIn MailIn   `toml:"mail_in"`
	Routes []*Route `toml:"route"`
}

type Log struct {
	Level  string `toml:"level"`  // debug|info|warn|error
	Format string `toml:"format"` // text|json
}

type IMAP struct {
	Server             string `toml:"server"`
	Username           string `toml:"username"`
	Password           string `toml:"password"`
	Folder             string `toml:"folder"`
	TLS                string `toml:"tls"`
	InsecureSkipVerify bool   `toml:"insecure_skip_verify"`
	PollInterval       string `toml:"poll_interval"`
	ReconnectInterval  string `toml:"reconnect_interval"`
}

type SMTP struct {
	Server             string `toml:"server"`
	Username           string `toml:"username"`
	Password           string `toml:"password"`
	From               string `toml:"from"`
	FromName           string `toml:"from_name"`
	TLS                string `toml:"tls"`
	InsecureSkipVerify bool   `toml:"insecure_skip_verify"`
}

type Bridge struct {
	URL               string `toml:"url"`
	Token             string `toml:"token"`
	ReconnectInterval string `toml:"reconnect_interval"`
}

type MailIn struct {
	UsernameFormat     string   `toml:"username_format"`
	IncludeSubject     bool     `toml:"include_subject"`
	IncludeAttachments bool     `toml:"include_attachments"`
	MaxAttachmentMB    int      `toml:"max_attachment_mb"`
	AllowedFrom        []string `toml:"allowed_from"`
}

type Route struct {
	Gateway       string   `toml:"gateway"`
	Channel       string   `toml:"channel"`
	To            []string `toml:"to"`
	SubjectFormat string   `toml:"subject_format"`
	BodyFormat    string   `toml:"body_format"`
}

// Load reads the configuration file, expands ${ENV} variables in it and
// applies defaults.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	expanded := os.ExpandEnv(string(raw))

	var cfg Config
	if _, err := toml.Decode(expanded, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "text"
	}
	if c.IMAP.Folder == "" {
		c.IMAP.Folder = "INBOX"
	}
	if c.IMAP.TLS == "" {
		c.IMAP.TLS = TLSSSL
	}
	if c.IMAP.PollInterval == "" {
		c.IMAP.PollInterval = "30s"
	}
	if c.IMAP.ReconnectInterval == "" {
		c.IMAP.ReconnectInterval = "10s"
	}
	if c.SMTP.TLS == "" {
		c.SMTP.TLS = TLSSSL
	}
	if c.SMTP.FromName == "" {
		c.SMTP.FromName = "MatterMail"
	}
	if c.Bridge.ReconnectInterval == "" {
		c.Bridge.ReconnectInterval = "5s"
	}
	if c.MailIn.UsernameFormat == "" {
		c.MailIn.UsernameFormat = "{name} ({addr})"
	}
	if c.MailIn.MaxAttachmentMB == 0 {
		c.MailIn.MaxAttachmentMB = 10
	}
	for _, r := range c.Routes {
		if r.SubjectFormat == "" {
			r.SubjectFormat = "[{gateway}] {nick}"
		}
		if r.BodyFormat == "" {
			r.BodyFormat = "{nick}:\n\n{text}"
		}
	}
}

// Validate reports missing or inconsistent configuration.
func (c *Config) Validate() error {
	if c.IMAP.Server == "" {
		return fmt.Errorf("imap.server is required")
	}
	if c.IMAP.Username == "" || c.IMAP.Password == "" {
		return fmt.Errorf("imap.username and imap.password are required")
	}
	if err := validTLS(c.IMAP.TLS, "imap"); err != nil {
		return err
	}
	if err := validDuration(c.IMAP.PollInterval, "imap.poll_interval"); err != nil {
		return err
	}
	if err := validDuration(c.IMAP.ReconnectInterval, "imap.reconnect_interval"); err != nil {
		return err
	}
	if c.SMTP.Server == "" {
		return fmt.Errorf("smtp.server is required")
	}
	if c.SMTP.From == "" {
		return fmt.Errorf("smtp.from is required")
	}
	if err := validTLS(c.SMTP.TLS, "smtp"); err != nil {
		return err
	}
	if c.Bridge.URL == "" {
		return fmt.Errorf("matterbridge.url is required")
	}
	if err := validDuration(c.Bridge.ReconnectInterval, "matterbridge.reconnect_interval"); err != nil {
		return err
	}
	if len(c.Routes) == 0 {
		return fmt.Errorf("at least one [[route]] is required")
	}
	for i, r := range c.Routes {
		if r.Gateway == "" {
			return fmt.Errorf("route[%d]: gateway is required", i)
		}
		if len(r.To) == 0 {
			return fmt.Errorf("route[%d]: to is required", i)
		}
	}
	return nil
}

func validTLS(mode, section string) error {
	switch mode {
	case TLSSSL, TLSStartTLS, TLSNone:
		return nil
	default:
		return fmt.Errorf("%s.tls: invalid value %q (use ssl, starttls or none)", section, mode)
	}
}

func validDuration(s, name string) error {
	if _, err := time.ParseDuration(s); err != nil {
		return fmt.Errorf("%s: invalid duration %q", name, s)
	}
	return nil
}

func (c *Config) PollInterval() time.Duration {
	d, _ := time.ParseDuration(c.IMAP.PollInterval)
	return d
}

// IMAPReconnectInterval is the delay before reconnecting a broken IMAP session.
func (c *Config) IMAPReconnectInterval() time.Duration {
	d, _ := time.ParseDuration(c.IMAP.ReconnectInterval)
	return d
}

// BridgeReconnectInterval is the delay before reconnecting the matterbridge stream.
func (c *Config) BridgeReconnectInterval() time.Duration {
	d, _ := time.ParseDuration(c.Bridge.ReconnectInterval)
	return d
}

// SMTPTimeout is the maximum time allowed for one SMTP transaction.
const SMTPTimeout = 60 * time.Second

// AllowedFromMatch reports whether addr matches any allowed_from pattern.
// An empty pattern list allows everyone. Patterns (case-insensitive):
//
//	alice@example.org   exact address
//	@example.org        any address at the domain
//	*.example.org       any address at the domain or a subdomain of it
//	*                   everyone
func AllowedFromMatch(patterns []string, addr string) bool {
	if len(patterns) == 0 {
		return true
	}
	addr = strings.ToLower(strings.TrimSpace(addr))
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		switch {
		case p == "*":
			return true
		case p == addr:
			return true
		case strings.HasPrefix(p, "@"):
			if domainOf(addr) == p[1:] {
				return true
			}
		case strings.HasPrefix(p, "*."):
			d := domainOf(addr)
			for d != "" {
				if d == p[2:] {
					return true
				}
				if i := strings.Index(d, "."); i >= 0 {
					d = d[i+1:]
				} else {
					d = ""
				}
			}
		}
	}
	return false
}

func domainOf(addr string) string {
	i := strings.LastIndex(addr, "@")
	if i < 0 {
		return ""
	}
	return addr[i+1:]
}
