package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mattermail.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const minimalConfig = `
[imap]
server = "imap.example.com:993"
username = "bridge@example.com"
password = "secret"

[smtp]
server = "smtp.example.com:465"
username = "bridge@example.com"
password = "secret"
from = "bridge@example.com"

[matterbridge]
url = "http://127.0.0.1:4242"

[[route]]
gateway = "friends"
to = ["alice@example.org"]
`

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(writeTemp(t, minimalConfig))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IMAP.Folder != "INBOX" {
		t.Errorf("folder = %q, want INBOX", cfg.IMAP.Folder)
	}
	if cfg.IMAP.TLS != TLSSSL {
		t.Errorf("imap tls = %q, want ssl", cfg.IMAP.TLS)
	}
	if cfg.SMTP.TLS != TLSSSL {
		t.Errorf("smtp tls = %q, want ssl", cfg.SMTP.TLS)
	}
	if cfg.PollInterval().String() != "30s" {
		t.Errorf("poll interval = %v, want 30s", cfg.PollInterval())
	}
	if cfg.MailIn.MaxAttachmentMB != 10 {
		t.Errorf("max attachment = %d, want 10", cfg.MailIn.MaxAttachmentMB)
	}
	if cfg.MailIn.UsernameFormat != "{name} ({addr})" {
		t.Errorf("username format = %q", cfg.MailIn.UsernameFormat)
	}
	if cfg.Routes[0].SubjectFormat != "[{gateway}] {nick}" {
		t.Errorf("subject format = %q", cfg.Routes[0].SubjectFormat)
	}
}

func TestEnvExpansion(t *testing.T) {
	t.Setenv("MM_TEST_PASS", "envsecret")
	path := writeTemp(t, `
[imap]
server = "imap.example.com:993"
username = "bridge@example.com"
password = "${MM_TEST_PASS}"

[smtp]
server = "smtp.example.com:465"
from = "bridge@example.com"

[matterbridge]
url = "http://127.0.0.1:4242"

[[route]]
gateway = "friends"
to = ["alice@example.org"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IMAP.Password != "envsecret" {
		t.Errorf("password = %q, want envsecret", cfg.IMAP.Password)
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string
	}{
		{"no imap server", func(c *Config) { c.IMAP.Server = "" }, "imap.server"},
		{"no smtp from", func(c *Config) { c.SMTP.From = "" }, "smtp.from"},
		{"no url", func(c *Config) { c.Bridge.URL = "" }, "matterbridge.url"},
		{"no routes", func(c *Config) { c.Routes = nil }, "route"},
		{"bad tls", func(c *Config) { c.IMAP.TLS = "maybe" }, "imap.tls"},
		{"bad duration", func(c *Config) { c.IMAP.PollInterval = "soon" }, "poll_interval"},
		{"route without to", func(c *Config) { c.Routes[0].To = nil }, "to is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeTemp(t, minimalConfig))
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(cfg)
			err = cfg.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestAllowedFromMatch(t *testing.T) {
	tests := []struct {
		patterns []string
		addr     string
		want     bool
	}{
		{nil, "anyone@anywhere.org", true},
		{[]string{"*"}, "anyone@anywhere.org", true},
		{[]string{"alice@example.org"}, "alice@example.org", true},
		{[]string{"alice@example.org"}, "bob@example.org", false},
		{[]string{"Alice@Example.ORG"}, "alice@example.org", true},
		{[]string{"@example.org"}, "bob@example.org", true},
		{[]string{"@example.org"}, "bob@sub.example.org", false},
		{[]string{"*.example.org"}, "bob@sub.example.org", true},
		{[]string{"*.example.org"}, "bob@example.org", true},
		{[]string{"*.example.org"}, "bob@evil.org", false},
		{[]string{"@a.org", "@b.org"}, "x@b.org", true},
	}
	for _, tt := range tests {
		if got := AllowedFromMatch(tt.patterns, tt.addr); got != tt.want {
			t.Errorf("AllowedFromMatch(%v, %s) = %v, want %v", tt.patterns, tt.addr, got, tt.want)
		}
	}
}
