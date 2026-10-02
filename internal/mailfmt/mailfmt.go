// Package mailfmt converts between raw MIME messages and matterbridge
// message representations.
package mailfmt

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"

	// Register additional charset decoders (iso-8859-*, windows-*, ...).
	_ "github.com/emersion/go-message/charset"
	"github.com/k3a/html2text"
)

// LoopMarkerHeader is set on every email mattermail sends; inbound mail
// carrying it is skipped to prevent bridging loops.
const LoopMarkerHeader = "X-Mattermail-Sent"

// maxTextBytes caps how much of a text part is read.
const maxTextBytes = 2 << 20 // 2 MiB

// Attachment is a decoded email attachment.
type Attachment struct {
	Name        string
	ContentType string
	Data        []byte
}

// InboundMail is the distilled content of a received email.
type InboundMail struct {
	MessageID    string
	InReplyTo    string
	FromName     string
	FromAddr     string
	Subject      string
	Text         string
	ToAddrs      []string
	Attachments  []Attachment
	LoopMarker   bool
	SkippedFiles []string // attachments dropped because of the size cap
}

// ParseMail decodes a raw RFC 5322 message. maxAttachmentBytes <= 0 means
// attachments are dropped.
func ParseMail(raw []byte, maxAttachmentBytes int64) (*InboundMail, error) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse mime: %w", err)
	}

	in := &InboundMail{
		MessageID:  mr.Header.Get("Message-ID"),
		InReplyTo:  mr.Header.Get("In-Reply-To"),
		LoopMarker: mr.Header.Get(LoopMarkerHeader) != "",
	}

	if subj, err := mr.Header.Subject(); err == nil {
		in.Subject = subj
	}
	if from, err := mr.Header.AddressList("From"); err == nil && len(from) > 0 {
		in.FromName = from[0].Name
		in.FromAddr = from[0].Address
	}
	for _, key := range []string{"To", "Cc", "Delivered-To"} {
		list, err := mr.Header.AddressList(key)
		if err != nil {
			continue
		}
		for _, a := range list {
			in.ToAddrs = append(in.ToAddrs, a.Address)
		}
	}

	var htmlBody []byte
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			if message.IsUnknownCharset(err) {
				continue
			}
			return in, fmt.Errorf("read part: %w", err)
		}

		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			ct, params, _ := h.ContentType()
			switch {
			case strings.HasPrefix(ct, "text/plain"):
				if in.Text == "" {
					b, _ := io.ReadAll(io.LimitReader(p.Body, maxTextBytes))
					in.Text = normalizeNewlines(string(b))
				}
			case strings.HasPrefix(ct, "text/html"):
				if htmlBody == nil {
					b, _ := io.ReadAll(io.LimitReader(p.Body, maxTextBytes))
					htmlBody = b
				}
			default:
				// Non-text inline part with a filename (e.g. inline
				// images): treat as attachment.
				name := attachmentName(params)
				if name != "" {
					in.attach(p.Body, name, ct, maxAttachmentBytes)
				}
			}
		case *mail.AttachmentHeader:
			name, _ := h.Filename()
			if name == "" {
				name = "attachment"
			}
			ct, _, _ := h.ContentType()
			in.attach(p.Body, name, ct, maxAttachmentBytes)
		}
	}

	if in.Text == "" && htmlBody != nil {
		in.Text = normalizeNewlines(html2text.HTML2Text(string(htmlBody)))
	}
	return in, nil
}

func (in *InboundMail) attach(r io.Reader, name, contentType string, maxBytes int64) {
	if maxBytes <= 0 {
		in.SkippedFiles = append(in.SkippedFiles, name)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		in.SkippedFiles = append(in.SkippedFiles, name)
		return
	}
	if int64(len(data)) > maxBytes {
		in.SkippedFiles = append(in.SkippedFiles, name)
		return
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	in.Attachments = append(in.Attachments, Attachment{Name: name, ContentType: contentType, Data: data})
}

func attachmentName(params map[string]string) string {
	return params["name"]
}

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// OutgoingMail is an email waiting to be serialized.
type OutgoingMail struct {
	FromName    string
	FromAddr    string
	To          []string
	Subject     string
	Body        string
	Attachments []Attachment
}

// ComposeMail serializes an email to raw RFC 5322 bytes, including the
// loop-prevention marker header.
func ComposeMail(o OutgoingMail) ([]byte, error) {
	var h mail.Header
	h.SetDate(time.Now())
	h.SetSubject(o.Subject)
	h.SetAddressList("From", []*mail.Address{{Name: o.FromName, Address: o.FromAddr}})
	h.SetAddressList("To", addresses(o.To))
	h.Set(LoopMarkerHeader, "1")
	h.Set("X-Mailer", "mattermail")
	if err := h.GenerateMessageID(); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	mw, err := mail.CreateWriter(&buf, h)
	if err != nil {
		return nil, err
	}

	ih := message.Header{}
	ih.Set("Content-Type", "text/plain; charset=utf-8")
	tw, err := mw.CreateSingleInline(mail.InlineHeader{Header: ih})
	if err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(o.Body)); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}

	for _, a := range o.Attachments {
		var ah message.Header
		ah.Set("Content-Type", a.ContentType)
		att := mail.AttachmentHeader{Header: ah}
		att.SetFilename(a.Name)
		aw, err := mw.CreateAttachment(att)
		if err != nil {
			return nil, err
		}
		if _, err := aw.Write(a.Data); err != nil {
			return nil, err
		}
		if err := aw.Close(); err != nil {
			return nil, err
		}
	}

	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func addresses(addrs []string) []*mail.Address {
	out := make([]*mail.Address, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, &mail.Address{Address: a})
	}
	return out
}
