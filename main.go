// aboutmeow keeps a permanent WhatsApp "About" bio alive.
// WhatsApp only shows the About text for a limited time (like a Discord status,
// it expires after max ~30 days). This app re-sets the bio automatically before
// it can expire: it connects with whatsmeow, pushes the configured text,
// and renews it on a schedule and after every reconnect.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"go.mau.fi/util/jsontime"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

const aboutMutationMaxRetries = 3
const aboutRetryBaseDelay = 10 * time.Second

// renewalInterval is how often the bio is proactively refreshed.
// WhatsApp's About text expires after at most 30 days; refreshing daily
// keeps it permanently visible with minimal server traffic.
const renewalInterval = 24 * time.Hour

const renewalJitter = 15 * time.Minute

func versionString() string { return "1.0.0" }

func defaultDBPath() string {
	// Prefer a stable location under the invoking user's home directory.
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return filepath.Join(u.HomeDir, ".local", "share", "aboutmeow", "session.db")
	}
	return "aboutmeow.db"
}

func main() {
	var (
		bio         = flag.String("bio", "", "The bio text to keep alive. Required unless -version is given.")
		emojiFlag   = flag.String("emoji", "", "Optional emoji shown next to the About text.")
		durationOpt = flag.String("duration", "86400", "About expiry in seconds as accepted by WhatsApp (86400 = 1 day). The daemon renews daily anyway, so one day is the safe default. 0 = WhatsApp server default (~30 days), which some server versions reject with 400.")
		dbFlag      = flag.String("db", defaultDBPath(), "Path to the SQLite session database.")
		phoneFlag   = flag.String("phone", "", "Phone number in international format (e.g. 491234567890). Enables pairing by pair code instead of QR.")
		daemonFlag  = flag.Bool("daemon", false, "Stay connected after the initial update and keep renewing the bio forever. Without this flag the app exits after the first renewal.")
		logLevel    = flag.String("log", "info", "Log level: debug, info, warn or error.")
		showVersion = flag.Bool("version", false, "Print version and exit.")
	)
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "aboutmeow %s - keeps a WhatsApp About (bio) alive by renewing it automatically.\n\n", versionString())
		fmt.Fprintf(out, "Usage:\n  aboutmeow -bio \"your permanent bio\" [flags]\n\nFlags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(out, `
Examples:
  # First run (pairs with the WhatsApp server, prints a QR code in the terminal):
  aboutmeow -bio "Building things at night."

  # Pair with a phone number code instead of scanning a QR code:
  aboutmeow -bio "Building things at night." -phone 491234567890

  # After pairing, run continuously and renew the bio every day:
  aboutmeow -bio "Building things at night." -daemon

  # Change the bio on an existing session:
  aboutmeow -bio "New bio text." -daemon
`)
	}
	flag.Parse()

	if *showVersion {
		fmt.Println("aboutmeow", versionString())
		os.Exit(0)
	}

	logger := newLogger(*logLevel)

	if *bio == "" {
		fmt.Fprintln(os.Stderr, "Error: -bio is required (the text that should stay visible).")
		flag.Usage()
		os.Exit(2)
	}

	duration, err := parseDurationSeconds(*durationOpt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid -duration value %q: %v\n", *durationOpt, err)
		os.Exit(2)
	}

	if err := run(logger, *bio, *emojiFlag, duration, *dbFlag, *phoneFlag, *daemonFlag); err != nil {
		logger.Error("Fatal error", slog.Any("error", err))
		os.Exit(1)
	}
}

func parseDurationSeconds(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("must be an integer number of seconds")
	}
	if v < 0 {
		return 0, fmt.Errorf("must not be negative")
	}
	return v, nil
}

func run(logger *slog.Logger, bio, emoji string, durationSec int64, dbPath, phone string, daemon bool) error {
	client, err := connectClient(context.Background(), logger, dbPath, phone)
	if err != nil {
		return err
	}
	defer client.Disconnect()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Push the initial bio immediately after the first successful (re)connect:
	// connectClient blocks until the session is logged in and Connected was fired.
	if err := setAboutWithRetry(ctx, client, logger, bio, emoji, durationSec); err != nil {
		if !daemon {
			return err
		}
		// In daemon mode keep running: the hourly tick below will retry.
		logger.Error("Initial About update failed, daemon keeps running and will retry", slog.Any("error", err))
	} else {
		logger.Info("About text is now visible again", slog.String("text", bio))
	}

	if !daemon {
		return nil
	}

	// Re-set the bio whenever WhatsApp drops and restores the connection,
	// and independently on the daily renewal tick. After failures the next
	// attempt happens on the next hourly tick (retryAfter tracks this).
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	retryAfterFailure := false
	nextRenewal := time.Now().Add(renewalInterval)

	for {
		select {
		case <-ctx.Done():
			logger.Info("Shutting down")
			return nil
		case <-ticker.C:
			if !client.IsConnected() || !client.IsLoggedIn() {
				logger.Debug("Not connected yet, skipping tick")
				continue
			}
			retryNow := time.Now().After(nextRenewal) || retryAfterFailure
			if !retryNow {
				continue
			}
			if err := setAboutWithRetry(ctx, client, logger, bio, emoji, durationSec); err != nil {
				logger.Error("Renewal failed, will retry next tick", slog.Any("error", err))
				retryAfterFailure = true
			} else {
				retryAfterFailure = false
				nextRenewal = time.Now().Add(renewalInterval)
			}
		case <-connectionRefresh:
			if err := setAboutWithRetry(ctx, client, logger, bio, emoji, durationSec); err != nil {
				logger.Error("Reconnect renewal failed", slog.Any("error", err))
			} else {
				nextRenewal = time.Now().Add(renewalInterval)
			}
		}
	}
}

// connectionRefresh is signalled after the client (re)connects and is logged in,
// so the bio can be pushed again without waiting for the next daily tick.
var connectionRefresh = make(chan struct{}, 8)

func setAboutWithRetry(ctx context.Context, client *whatsmeow.Client, logger *slog.Logger, bio, emoji string, durationSec int64) error {
	var lastErr error
	for attempt := 1; attempt <= aboutMutationMaxRetries; attempt++ {
		if attempt > 1 {
			delay := aboutRetryBaseDelay * time.Duration(attempt-1)
			logger.Warn("Retrying About update", slog.Int("attempt", attempt), slog.Duration("delay", delay))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		err := setAbout(ctx, client, logger, bio, emoji, durationSec)
		if err == nil {
			return nil
		}
		lastErr = err
		logger.Warn("About update failed", slog.Int("attempt", attempt), slog.Any("error", err))
	}
	return fmt.Errorf("updating About failed after %d attempts: %w", aboutMutationMaxRetries, lastErr)
}

func setAbout(ctx context.Context, client *whatsmeow.Client, logger *slog.Logger, bio, emoji string, durationSec int64) error {
	attempts := []int64{durationSec}
	if durationSec == 0 {
		// Some server versions reject duration=0 with "graphql error: 400".
		// WhatsApp Web itself defaults to one day, so fall back to that and
		// the full 30 days.
		attempts = append(attempts, 86400, 2592000)
	}
	var lastErr error
	for _, dur := range attempts {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := client.SetStatusMessage(ctx, makeAboutInput(bio, emoji, dur))
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		logger.Warn("About update rejected", slog.Int64("duration_sec", dur), slog.Any("error", err))
	}
	return lastErr
}

func makeAboutInput(bio, emoji string, durationSec int64) types.SetStatusInput {
	input := types.SetStatusInput{
		Text:     &bio,
		Duration: jsontime.SInt(int(durationSec)),
	}
	if emoji != "" {
		input.Emoji = &types.SetStatusEmoji{Content: emoji}
	}
	return input
}

func newLogger(level string) *slog.Logger {
	var programLevel = new(slog.LevelVar)
	switch level {
	case "debug":
		programLevel.Set(slog.LevelDebug)
	case "warn":
		programLevel.Set(slog.LevelWarn)
	case "error":
		programLevel.Set(slog.LevelError)
	default:
		programLevel.Set(slog.LevelInfo)
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: programLevel}))
}