// Package mbapi is a client for matterbridge's built-in api bridge
// (https://github.com/42wim/matterbridge/wiki/Api).
//
// Messages are injected with POST /api/message and received over the
// WebSocket endpoint /api/websocket. The /api/stream endpoint is avoided on
// purpose: it drains a shared ring buffer and is not multi-consumer safe.
package mbapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"log/slog"
)

// FileInfo mirrors matterbridge's config.FileInfo. Data travels base64
// encoded on the wire, which encoding/json handles transparently for []byte.
type FileInfo struct {
	Name    string `json:"name"`
	Data    []byte `json:"data,omitempty"`
	Comment string `json:"comment,omitempty"`
	URL     string `json:"url,omitempty"`
	Size    int64  `json:"size,omitempty"`
	SHA     string `json:"sha,omitempty"`
	Avatar  string `json:"avatar,omitempty"`
}

// Message is a subset of matterbridge's config.Message JSON format.
type Message struct {
	Text     string                `json:"text"`
	Channel  string                `json:"channel"`
	Username string                `json:"username"`
	UserID   string                `json:"userid"`
	Avatar   string                `json:"avatar"`
	Account  string                `json:"account"`
	Event    string                `json:"event"`
	Protocol string                `json:"protocol"`
	Gateway  string                `json:"gateway"`
	ID       string                `json:"id"`
	Extra    map[string][]FileInfo `json:"extra,omitempty"`
}

// Files returns the file attachments of the message.
func (m *Message) Files() []FileInfo {
	if m.Extra == nil {
		return nil
	}
	return m.Extra["file"]
}

// Client talks to one matterbridge api bridge account.
type Client struct {
	base              *url.URL
	token             string
	hc                *http.Client
	log               *slog.Logger
	reconnectInterval time.Duration
}

// New creates a client for the api bridge at rawURL (e.g. http://127.0.0.1:4242).
func New(rawURL, token string, reconnectInterval time.Duration, log *slog.Logger) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse matterbridge url: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("matterbridge.url: unsupported scheme %q", u.Scheme)
	}
	if reconnectInterval <= 0 {
		reconnectInterval = 5 * time.Second
	}
	return &Client{
		base:              u,
		token:             token,
		hc:                &http.Client{Timeout: 30 * time.Second},
		log:               log,
		reconnectInterval: reconnectInterval,
	}, nil
}

// PostMessage injects a message into matterbridge. Note that the server
// ignores the Channel field and relays the message to every gateway that
// contains this api account.
func (c *Client) PostMessage(ctx context.Context, msg Message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	u := *c.base
	u.Path = strings.TrimSuffix(u.Path, "/") + "/api/message"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("matterbridge POST /api/message: %s", resp.Status)
	}
	return nil
}

// Stream connects to /api/websocket and calls onMsg for every relayed chat
// message. It reconnects automatically until ctx is cancelled; event frames
// (api_connected greeting, join/leave notices, ...) are filtered out.
func (c *Client) Stream(ctx context.Context, onMsg func(Message)) error {
	for {
		if err := c.streamOnce(ctx, onMsg); err != nil && ctx.Err() == nil {
			c.log.Warn("matterbridge stream error, reconnecting", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.reconnectInterval):
		}
	}
}

func (c *Client) streamOnce(ctx context.Context, onMsg func(Message)) error {
	u := *c.base
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/api/websocket"

	header := http.Header{}
	if c.token != "" {
		header.Set("Authorization", "Bearer "+c.token)
	}

	dialer := &websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, _, err := dialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return fmt.Errorf("dial %s: %w", u.String(), err)
	}
	defer conn.Close()
	c.log.Info("connected to matterbridge", "url", c.base.String())

	done := make(chan struct{})
	go func() {
		// Close the connection when the context is cancelled so that a
		// blocked ReadMessage returns.
		<-ctx.Done()
		conn.Close()
		close(done)
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			select {
			case <-done:
				return ctx.Err()
			default:
				return fmt.Errorf("read websocket: %w", err)
			}
		}
		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			c.log.Warn("undecodable websocket frame", "err", err)
			continue
		}
		if msg.Event != "" {
			continue
		}
		onMsg(msg)
	}
}
