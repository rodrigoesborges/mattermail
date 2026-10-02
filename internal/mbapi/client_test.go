package mbapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"log/slog"
)

func TestPostMessage(t *testing.T) {
	var gotBody Message
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/message" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL, "sekrit", time.Second, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	msg := Message{Text: "hello", Username: "alice", Extra: map[string][]FileInfo{
		"file": {{Name: "a.bin", Data: []byte{1, 2, 3}}},
	}}
	if err := c.PostMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer sekrit" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody.Text != "hello" || gotBody.Username != "alice" {
		t.Errorf("body = %+v", gotBody)
	}
	files := gotBody.Files()
	if len(files) != 1 || files[0].Name != "a.bin" || len(files[0].Data) != 3 {
		t.Errorf("files = %+v", files)
	}
}

func TestPostMessageError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "", time.Second, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.PostMessage(context.Background(), Message{Text: "x"}); err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestStreamReceivesMessages(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/websocket" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sekrit" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// greeting, then a normal message, then an event frame (must be skipped)
		for _, frame := range []Message{
			{Event: "api_connected"},
			{Text: "hi from chat", Username: "bob", Gateway: "friends"},
			{Event: "join", Username: "bob"},
		} {
			if err := conn.WriteJSON(frame); err != nil {
				return
			}
		}
		// keep the connection open until the client goes away
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "sekrit", 50*time.Millisecond, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	received := make(chan Message, 4)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Stream(ctx, func(m Message) { received <- m })
	}()

	select {
	case m := <-received:
		if m.Text != "hi from chat" || m.Username != "bob" {
			t.Errorf("received = %+v", m)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("no message received over websocket")
	}

	// only the chat message should arrive, not the event frames
	select {
	case m := <-received:
		t.Errorf("unexpected extra message: %+v", m)
	default:
	}

	cancel()
	if err := <-errCh; err != context.Canceled && err != context.DeadlineExceeded {
		t.Logf("stream returned %v", err)
	}
}

func TestNewBadURL(t *testing.T) {
	if _, err := New("ftp://example.com", "", time.Second, nil); err == nil {
		t.Fatal("expected error for non-http scheme")
	}
}
