package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"

	"github.com/ricardo-duarte-av/matrix-youtube-preview/internal/youtube"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config.yaml")
	testURL := flag.String("test-url", "", "fetch and print a preview for this YouTube URL, then exit (no Matrix connection)")
	flag.Parse()

	log := zerolog.New(zerolog.NewConsoleWriter(func(w *zerolog.ConsoleWriter) {
		w.Out = os.Stderr
		w.TimeFormat = time.DateTime
	})).With().Timestamp().Logger()

	if *testURL != "" {
		os.Exit(runTest(log, *configPath, *testURL))
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load config")
	}
	if lvl, err := zerolog.ParseLevel(cfg.LogLevel); err == nil {
		log = log.Level(lvl)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, log, cfg); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal().Err(err).Msg("Bot stopped")
	}
	log.Info().Msg("Bye")
}

// runTest fetches a preview without connecting to Matrix. Cookies/user agent
// are taken from the config file if it exists.
func runTest(log zerolog.Logger, configPath, url string) int {
	var cookies, ua string
	if cfg, err := LoadConfig(configPath); err == nil {
		cookies, ua = cfg.YouTube.CookiesFile, cfg.YouTube.UserAgent
	}
	if env := os.Getenv("YT_COOKIES_FILE"); env != "" {
		cookies = env
	}
	if env := os.Getenv("YT_USER_AGENT"); env != "" {
		ua = env
	}
	yt, err := youtube.NewClient(ua, cookies)
	if err != nil {
		log.Error().Err(err).Msg("YouTube client")
		return 1
	}
	id := youtube.VideoID(url)
	if id == "" {
		log.Error().Str("url", url).Msg("Not a YouTube video URL")
		return 1
	}
	v, err := yt.Fetch(context.Background(), id)
	if err != nil {
		log.Error().Err(err).Msg("Fetch failed")
		return 1
	}
	fmt.Println(v.Plain())
	fmt.Println("\nHTML:\n" + v.HTML())
	fmt.Printf("\ncookies=%v duration=%v thumbnails=%v\n", yt.HasCookies(), v.Duration, v.ThumbnailCandidates()[0])
	return 0
}

func run(ctx context.Context, log zerolog.Logger, cfg *Config) error {
	yt, err := youtube.NewClient(cfg.YouTube.UserAgent, cfg.YouTube.CookiesFile)
	if err != nil {
		return err
	}
	if yt.HasCookies() {
		log.Info().Str("file", cfg.YouTube.CookiesFile).Msg("Using YouTube cookies")
	}

	if dir := filepath.Dir(cfg.Database); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create database dir: %w", err)
		}
	}

	client, err := mautrix.NewClient(cfg.Server, "", "")
	if err != nil {
		return err
	}
	client.Log = log

	b := &Bot{client: client, yt: yt, cfg: cfg, log: log, startTime: time.Now()}

	syncer := client.Syncer.(*mautrix.DefaultSyncer)
	// The very first sync (no since token) is skipped entirely by
	// DontProcessOldEvents so we don't answer old messages. Join the invites
	// it contains first, so invites received while offline aren't lost.
	syncer.OnSync(func(ctx context.Context, resp *mautrix.RespSync, since string) bool {
		if since == "" {
			for roomID := range resp.Rooms.Invite {
				go b.join(ctx, roomID, "")
			}
		}
		return true
	})
	syncer.OnSync(client.DontProcessOldEvents)
	syncer.OnEventType(event.StateMember, b.handleMember)
	syncer.OnEventType(event.EventMessage, b.handleMessage)

	ch, err := cryptohelper.NewCryptoHelper(client, []byte(cfg.PickleKey), cfg.Database)
	if err != nil {
		return fmt.Errorf("crypto helper: %w", err)
	}
	ch.LoginAs = &mautrix.ReqLogin{
		Type:                     mautrix.AuthTypePassword,
		Identifier:               mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: cfg.User},
		Password:                 cfg.Password,
		InitialDeviceDisplayName: cfg.DeviceName,
	}
	ch.DecryptErrorCallback = func(evt *event.Event, err error) {
		if errors.Is(err, context.Canceled) {
			return // shutting down
		}
		log.Warn().Err(err).Stringer("room_id", evt.RoomID).Stringer("event_id", evt.ID).Msg("Failed to decrypt event")
	}
	if err := ch.Init(ctx); err != nil {
		return fmt.Errorf("crypto init / login: %w", err)
	}
	defer ch.Close()
	client.Crypto = ch

	log.Info().Stringer("user_id", client.UserID).Stringer("device_id", client.DeviceID).Msg("Logged in, syncing")

	for {
		err := client.SyncWithContext(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Error().Err(err).Msg("Sync failed, retrying in 10s")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}
