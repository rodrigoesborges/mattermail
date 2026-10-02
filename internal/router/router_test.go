package router

import (
	"strings"
	"testing"

	"github.com/rodrigoesborges/mattermail/internal/config"
	"github.com/rodrigoesborges/mattermail/internal/mailfmt"
	"github.com/rodrigoesborges/mattermail/internal/mbapi"
)

func inboundSubjectText(subject, text string) *mailfmt.InboundMail {
	return &mailfmt.InboundMail{Subject: subject, Text: text}
}

func testConfig() *config.Config {
	return &config.Config{
		IMAP:   config.IMAP{Username: "bridge@example.com"},
		SMTP:   config.SMTP{From: "bridge@example.com", FromName: "MatterMail"},
		Bridge: config.Bridge{URL: "http://127.0.0.1:4242"},
		MailIn: config.MailIn{
			UsernameFormat:     "{name} ({addr})",
			IncludeSubject:     true,
			IncludeAttachments: true,
			MaxAttachmentMB:    10,
		},
		Routes: []*config.Route{
			{Gateway: "friends", To: []string{"alice@example.org"}, SubjectFormat: "[{gateway}] {nick}", BodyFormat: "{nick}:\n\n{text}"},
			{Gateway: "work", Channel: "api", To: []string{"boss@corp.com"}},
		},
	}
}

func TestRenderUsername(t *testing.T) {
	if got := renderUsername("{name} ({addr})", "Alice", "a@b.c"); got != "Alice (a@b.c)" {
		t.Errorf("got %q", got)
	}
	if got := renderUsername("{name} ({addr})", "", "a@b.c"); got != "a@b.c" {
		t.Errorf("empty name should fall back to addr, got %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("é", 300)
	got := truncate(long, 100)
	if len([]rune(got)) != 103 { // 100 runes + "..."
		t.Errorf("len = %d", len([]rune(got)))
	}
}

func TestMatchRoute(t *testing.T) {
	r := New(testConfig(), nil, nil, nil)
	if r.matchRoute(mbapi.Message{Gateway: "friends"}) == nil {
		t.Error("gateway friends should match")
	}
	if r.matchRoute(mbapi.Message{Gateway: "other"}) != nil {
		t.Error("unknown gateway should not match")
	}
	if r.matchRoute(mbapi.Message{Gateway: "work", Channel: "api"}) == nil {
		t.Error("work + channel api should match")
	}
	if r.matchRoute(mbapi.Message{Gateway: "work", Channel: "other"}) != nil {
		t.Error("work + wrong channel should not match")
	}
	if r.matchRoute(mbapi.Message{Gateway: "FRIENDS"}) == nil {
		t.Error("gateway match should be case-insensitive")
	}
}

func TestInboundText(t *testing.T) {
	r := New(testConfig(), nil, nil, nil)
	r.cfg.MailIn.IncludeSubject = true
	got := r.inboundText(inboundSubjectText("Subj", "Body"))
	if got != "Subj\n\nBody" {
		t.Errorf("got %q", got)
	}
	r.cfg.MailIn.IncludeSubject = false
	got = r.inboundText(inboundSubjectText("Subj", "Body"))
	if got != "Body" {
		t.Errorf("got %q", got)
	}
}
