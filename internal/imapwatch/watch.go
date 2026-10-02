// Package imapwatch watches an IMAP folder for new mail and hands the raw
// messages to a callback. Mail is marked as seen only after the callback
// succeeds, giving at-least-once delivery.
package imapwatch

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"log/slog"

	"github.com/rodrigoesborges/mattermail/internal/config"
)

// Handler processes one raw MIME message. Returning an error leaves the
// message unseen so it is retried on the next poll.
type Handler func(ctx context.Context, raw []byte) error

// Watcher polls an IMAP folder for unseen mail.
type Watcher struct {
	cfg                config.IMAP
	pollInterval       time.Duration
	reconnectInterval  time.Duration
	maxAttachmentBytes int64
	log                *slog.Logger
	handler            Handler
}

// New creates a watcher. maxAttachmentBytes is the per-attachment cap handed
// to the MIME parser.
func New(cfg config.IMAP, pollInterval, reconnectInterval time.Duration, maxAttachmentBytes int64, log *slog.Logger, handler Handler) *Watcher {
	if pollInterval <= 0 {
		pollInterval = 30 * time.Second
	}
	if reconnectInterval <= 0 {
		reconnectInterval = 10 * time.Second
	}
	return &Watcher{
		cfg:                cfg,
		pollInterval:       pollInterval,
		reconnectInterval:  reconnectInterval,
		maxAttachmentBytes: maxAttachmentBytes,
		log:                log,
		handler:            handler,
	}
}

// Run blocks until ctx is cancelled, reconnecting on failures.
func (w *Watcher) Run(ctx context.Context) error {
	for {
		err := w.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.log.Error("imap session ended, reconnecting", "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(w.reconnectInterval):
		}
	}
}

func (w *Watcher) runOnce(ctx context.Context) error {
	c, err := w.connect(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = c.Logout().Wait()
	}()
	w.log.Info("imap connected", "server", w.cfg.Server, "folder", w.cfg.Folder)

	for {
		if err := w.poll(ctx, c); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(w.pollInterval):
		}
	}
}

func (w *Watcher) connect(ctx context.Context) (*imapclient.Client, error) {
	tlsCfg := &tls.Config{
		ServerName:         hostOf(w.cfg.Server),
		InsecureSkipVerify: w.cfg.InsecureSkipVerify, //nolint:gosec // opt-in
	}
	dial := func() (*imapclient.Client, error) {
		switch w.cfg.TLS {
		case config.TLSStartTLS:
			return imapclient.DialStartTLS(w.cfg.Server, &imapclient.Options{TLSConfig: tlsCfg})
		case config.TLSNone:
			return imapclient.DialInsecure(w.cfg.Server, &imapclient.Options{})
		default:
			return imapclient.DialTLS(w.cfg.Server, &imapclient.Options{TLSConfig: tlsCfg})
		}
	}
	_ = ctx // dial has no context support; guarded by reconnect loop

	c, err := dial()
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", w.cfg.Server, err)
	}
	if err := c.Login(w.cfg.Username, w.cfg.Password).Wait(); err != nil {
		c.Logout().Wait()
		return nil, fmt.Errorf("login: %w", err)
	}
	if _, err := c.Select(w.cfg.Folder, nil).Wait(); err != nil {
		c.Logout().Wait()
		return nil, fmt.Errorf("select %s: %w", w.cfg.Folder, err)
	}
	return c, nil
}

func (w *Watcher) poll(ctx context.Context, c *imapclient.Client) error {
	data, err := c.UIDSearch(&imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagSeen},
	}, nil).Wait()
	if err != nil {
		return fmt.Errorf("uid search: %w", err)
	}
	uids := data.AllUIDs()
	if len(uids) == 0 {
		return nil
	}
	w.log.Debug("unseen mail", "count", len(uids))

	section := &imap.FetchItemBodySection{Peek: true}
	fetchOpts := &imap.FetchOptions{
		UID:         true,
		Flags:       true,
		BodySection: []*imap.FetchItemBodySection{section},
	}
	cmd := c.Fetch(imap.UIDSetNum(uids...), fetchOpts)
	bufs, err := cmd.Collect()
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}

	var delivered []imap.UID
	for _, buf := range bufs {
		raw := buf.FindBodySection(section)
		if raw == nil {
			continue
		}
		if err := w.handler(ctx, raw); err != nil {
			w.log.Error("delivery failed, will retry next poll", "uid", buf.UID, "err", err)
			continue
		}
		delivered = append(delivered, buf.UID)
	}

	if len(delivered) > 0 {
		storeFlags := imap.StoreFlags{
			Op:     imap.StoreFlagsAdd,
			Flags:  []imap.Flag{imap.FlagSeen},
			Silent: true,
		}
		if err := c.Store(imap.UIDSetNum(delivered...), &storeFlags, nil).Close(); err != nil {
			return fmt.Errorf("store \\Seen: %w", err)
		}
	}
	return nil
}

func hostOf(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}
