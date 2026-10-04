package imapwatch

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"log/slog"

	"github.com/rodrigoesborges/mattermail/internal/config"
)

const testMail = `From: Alice <alice@example.org>
To: bridge@example.com
Subject: Integration test
Message-ID: <it-1@example.org>

Hello from the in-memory IMAP server.
`

func startMemServer(t *testing.T) string {
	t.Helper()
	memServer := imapmemserver.New()
	user := imapmemserver.NewUser("bridge@example.com", "pass")
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	memServer.AddUser(user)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memServer.NewSession(), nil, nil
		},
		InsecureAuth: true,
		Logger:       discardLogger{},
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln) //nolint:errcheck // test server
	t.Cleanup(func() { srv.Close() })

	// Expose the user for message delivery.
	deliver = func(raw []byte) error {
		cl, err := imapclient.DialInsecure(ln.Addr().String(), nil)
		if err != nil {
			return fmt.Errorf("dial: %w", err)
		}
		// Wrapped in a closure: `defer cl.Logout().Wait()` would evaluate
		// Logout() immediately and send LOGOUT before the LOGIN command.
		defer func() { _ = cl.Logout().Wait() }()
		if err := cl.Login("bridge@example.com", "pass").Wait(); err != nil {
			return fmt.Errorf("login: %w", err)
		}
		cmd := cl.Append("INBOX", int64(len(raw)), nil)
		if _, err := cmd.Write(raw); err != nil {
			return fmt.Errorf("append write: %w", err)
		}
		if err := cmd.Close(); err != nil {
			return fmt.Errorf("append close: %w", err)
		}
		if _, err := cmd.Wait(); err != nil {
			return fmt.Errorf("append wait: %w", err)
		}
		return nil
	}
	return ln.Addr().String()
}

var deliver func(raw []byte) error

type discardLogger struct{}

func (discardLogger) Printf(string, ...interface{}) {}

func TestWatcherDeliversAndMarksSeen(t *testing.T) {
	addr := startMemServer(t)

	// Deliver the mail before starting the watcher: two concurrent IMAP
	// session setups racing against the in-memory server is needlessly flaky.
	if err := deliver([]byte(testMail)); err != nil {
		t.Fatal(err)
	}

	got := make(chan []byte, 4)
	w := New(config.IMAP{
		Server:   addr,
		Username: "bridge@example.com",
		Password: "pass",
		Folder:   "INBOX",
		TLS:      config.TLSNone,
	}, 200*time.Millisecond, 100*time.Millisecond, 1<<20,
		slog.New(slog.DiscardHandler),
		func(_ context.Context, raw []byte) error {
			got <- raw
			return nil
		})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx) //nolint:errcheck // result checked via channels

	select {
	case raw := <-got:
		if !strings.Contains(string(raw), "Integration test") {
			t.Errorf("unexpected raw mail: %q", string(raw))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mail was not delivered to the handler")
	}

	// The message must be marked seen and not redelivered on later polls.
	select {
	case raw := <-got:
		t.Errorf("message redelivered: %q", string(raw))
	case <-time.After(time.Second):
	}
}

func TestWatcherRetriesFailedDelivery(t *testing.T) {
	addr := startMemServer(t)

	if err := deliver([]byte(testMail)); err != nil {
		t.Fatal(err)
	}

	var attempts int
	done := make(chan struct{})
	w := New(config.IMAP{
		Server:   addr,
		Username: "bridge@example.com",
		Password: "pass",
		Folder:   "INBOX",
		TLS:      config.TLSNone,
	}, 200*time.Millisecond, 100*time.Millisecond, 1<<20,
		slog.New(slog.DiscardHandler),
		func(_ context.Context, raw []byte) error {
			attempts++
			if attempts == 1 {
				return context.DeadlineExceeded // simulate downstream failure
			}
			close(done)
			return nil
		})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx) //nolint:errcheck

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("failed mail was never retried")
	}
}
