// Package router wires the mail side and the matterbridge side together:
// inbound mail becomes matterbridge messages, chat messages become email.
package router

import (
	"context"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"log/slog"

	"github.com/rodrigoesborges/mattermail/internal/config"
	"github.com/rodrigoesborges/mattermail/internal/mailfmt"
	"github.com/rodrigoesborges/mattermail/internal/mbapi"
	"github.com/rodrigoesborges/mattermail/internal/smtpsend"
)

// Router converts and routes messages in both directions.
type Router struct {
	cfg  *config.Config
	api  *mbapi.Client
	smtp *smtpsend.Sender
	log  *slog.Logger
}

// New creates a router.
func New(cfg *config.Config, api *mbapi.Client, sender *smtpsend.Sender, log *slog.Logger) *Router {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Router{cfg: cfg, api: api, smtp: sender, log: log}
}

// HandleMail is called by the IMAP watcher for every unseen message.
func (r *Router) HandleMail(ctx context.Context, raw []byte) error {
	in, err := mailfmt.ParseMail(raw, int64(r.cfg.MailIn.MaxAttachmentMB)<<20)
	if err != nil {
		// Unparsable mail would be retried forever: log and drop instead.
		r.log.Error("dropping unparsable mail", "err", err)
		return nil
	}

	if in.LoopMarker {
		r.log.Debug("skipping mail with loop marker", "message_id", in.MessageID)
		return nil
	}
	if isOwnAddress(in.FromAddr, r.cfg) {
		r.log.Debug("skipping mail from own address", "from", in.FromAddr)
		return nil
	}
	if !config.AllowedFromMatch(r.cfg.MailIn.AllowedFrom, in.FromAddr) {
		r.log.Info("skipping mail from non-allowed sender", "from", in.FromAddr)
		return nil
	}

	msg := mbapi.Message{
		Text:     r.inboundText(in),
		Username: renderUsername(r.cfg.MailIn.UsernameFormat, in.FromName, in.FromAddr),
		UserID:   in.FromAddr,
	}
	if r.cfg.MailIn.IncludeAttachments && len(in.Attachments) > 0 {
		files := make([]mbapi.FileInfo, 0, len(in.Attachments))
		for _, a := range in.Attachments {
			files = append(files, mbapi.FileInfo{
				Name: a.Name,
				Data: a.Data,
				Size: int64(len(a.Data)),
			})
		}
		msg.Extra = map[string][]mbapi.FileInfo{"file": files}
	}
	for _, name := range in.SkippedFiles {
		msg.Text += "\n[attachment dropped: " + name + "]"
	}

	r.log.Info("mail -> chat", "from", in.FromAddr, "subject", in.Subject)
	return r.api.PostMessage(ctx, msg)
}

// HandleChatMessage is called for every message received from matterbridge.
func (r *Router) HandleChatMessage(msg mbapi.Message) {
	route := r.matchRoute(msg)
	if route == nil {
		return
	}

	vars := templateVars{
		nick:    msg.Username,
		text:    msg.Text,
		gateway: msg.Gateway,
		channel: msg.Channel,
		account: msg.Account,
	}

	out := mailfmt.OutgoingMail{
		FromName: r.cfg.SMTP.FromName,
		FromAddr: r.cfg.SMTP.From,
		To:       route.To,
		Subject:  truncate(render(route.SubjectFormat, vars), 200),
		Body:     render(route.BodyFormat, vars),
	}

	for _, f := range msg.Files() {
		if len(f.Data) == 0 {
			if f.URL != "" {
				name := f.Name
				if name == "" {
					name = "file"
				}
				out.Body += fmt.Sprintf("\n%s: %s", name, f.URL)
			}
			continue
		}
		ct := mime.TypeByExtension(filepath.Ext(f.Name))
		if ct == "" {
			ct = "application/octet-stream"
		}
		out.Attachments = append(out.Attachments, mailfmt.Attachment{
			Name:        f.Name,
			ContentType: ct,
			Data:        f.Data,
		})
	}

	raw, err := mailfmt.ComposeMail(out)
	if err != nil {
		r.log.Error("compose mail failed", "err", err, "gateway", msg.Gateway)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.SMTPTimeout)
	defer cancel()
	if err := r.smtp.Send(ctx, raw, out.To); err != nil {
		r.log.Error("smtp send failed", "err", err, "gateway", msg.Gateway, "to", out.To)
		return
	}
	r.log.Info("chat -> mail", "gateway", msg.Gateway, "nick", msg.Username, "to", out.To)
}

func (r *Router) matchRoute(msg mbapi.Message) *config.Route {
	for _, route := range r.cfg.Routes {
		if !strings.EqualFold(route.Gateway, msg.Gateway) {
			continue
		}
		if route.Channel != "" && !strings.EqualFold(route.Channel, msg.Channel) {
			continue
		}
		return route
	}
	r.log.Debug("no route for message", "gateway", msg.Gateway, "channel", msg.Channel)
	return nil
}

func (r *Router) inboundText(in *mailfmt.InboundMail) string {
	var b strings.Builder
	if r.cfg.MailIn.IncludeSubject && strings.TrimSpace(in.Subject) != "" {
		b.WriteString(in.Subject)
		b.WriteString("\n\n")
	}
	b.WriteString(in.Text)
	return b.String()
}

func isOwnAddress(addr string, cfg *config.Config) bool {
	addr = strings.ToLower(strings.TrimSpace(addr))
	return addr == strings.ToLower(cfg.IMAP.Username) || addr == strings.ToLower(cfg.SMTP.From)
}

type templateVars struct {
	nick, text, gateway, channel, account string
}

func render(tpl string, v templateVars) string {
	r := strings.NewReplacer(
		"{nick}", v.nick,
		"{text}", v.text,
		"{gateway}", v.gateway,
		"{channel}", v.channel,
		"{account}", v.account,
	)
	return r.Replace(tpl)
}

func renderUsername(tpl, name, addr string) string {
	if name == "" || strings.EqualFold(name, addr) {
		return addr
	}
	return strings.NewReplacer("{name}", name, "{addr}", addr).Replace(tpl)
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:n])) + "..."
}
