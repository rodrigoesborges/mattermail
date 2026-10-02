// Command mattermail bridges email (SMTP/IMAP) to matterbridge gateways.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rodrigoesborges/mattermail/internal/config"
	"github.com/rodrigoesborges/mattermail/internal/imapwatch"
	"github.com/rodrigoesborges/mattermail/internal/mbapi"
	"github.com/rodrigoesborges/mattermail/internal/router"
	"github.com/rodrigoesborges/mattermail/internal/smtpsend"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "mattermail.toml", "path to the configuration file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("mattermail " + version)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mattermail: %v\n", err)
		os.Exit(1)
	}

	log := newLogger(cfg.Log)

	api, err := mbapi.New(cfg.Bridge.URL, cfg.Bridge.Token, cfg.BridgeReconnectInterval(), log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mattermail: %v\n", err)
		os.Exit(1)
	}

	rtr := router.New(cfg, api, smtpsend.New(cfg.SMTP, log), log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := api.Stream(ctx, rtr.HandleChatMessage); err != nil && ctx.Err() == nil {
			log.Error("matterbridge stream terminated", "err", err)
			stop()
		}
	}()

	watcher := imapwatch.New(cfg.IMAP, cfg.PollInterval(), cfg.IMAPReconnectInterval(),
		int64(cfg.MailIn.MaxAttachmentMB)<<20, log, rtr.HandleMail)
	if err := watcher.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("imap watcher terminated", "err", err)
		os.Exit(1)
	}

	log.Info("mattermail stopped")
}

func newLogger(cfg config.Log) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.Format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}
