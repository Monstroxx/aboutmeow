package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// connectClient creates the whatsmeow client, reuses a stored session when one
// exists, otherwise performs an interactive QR / pair-code login, and only
// returns once the client is connected and logged in.
func connectClient(ctx context.Context, logger *slog.Logger, dbPath, phone string) (*whatsmeow.Client, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	// NOTE: the dialect string must match the registered sqlite driver name.
	dbLog := waLog.Stdout("Database", "warn", true)
	container, err := sqlstore.New(ctx, "sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on", dbPath), dbLog)
	if err != nil {
		return nil, fmt.Errorf("open session database: %w", err)
	}

	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}

	freshLogin := device == nil || device.ID == nil
	if freshLogin {
		device = container.NewDevice()
		logger.Info("No stored session found, starting a new login.")
	}

	clientLog := waLog.Stdout("Client", "info", true)
	client := whatsmeow.NewClient(device, clientLog)
	client.EnableAutoReconnect = true
	client.InitialAutoReconnect = true

	// Watch connection state in all modes so the daemon can re-push the bio
	// after every reconnect.
	client.AddEventHandler(func(evt interface{}) {
		switch evt.(type) {
		case events.Connected:
			logger.Info("Connected to WhatsApp servers")
			select {
			case connectionRefresh <- struct{}{}:
			default:
			}
		case *events.LoggedOut:
			logger.Error("Session was logged out remotely. Delete the db file and pair again.")
		case events.StreamReplaced:
			logger.Error("Stream replaced: another client connected with the same session.")
		case *events.KeepAliveTimeout:
			logger.Debug("Keepalive timeout, waiting for auto-reconnect")
		}
	})

	if freshLogin {
		if phone != "" {
			if err := pairWithPhone(ctx, client, logger, phone); err != nil {
				return nil, err
			}
		} else {
			if err := pairWithQR(ctx, client, logger); err != nil {
				return nil, err
			}
		}
	} else {
		if err := client.Connect(); err != nil {
			return nil, fmt.Errorf("connect: %w", err)
		}
		if err := waitForLogin(ctx, client, 60*time.Second); err != nil {
			return nil, err
		}
	}

	return client, nil
}

// pairWithQR performs the interactive QR pairing flow.
func pairWithQR(ctx context.Context, client *whatsmeow.Client, logger *slog.Logger) error {
	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("open QR channel: %w", err)
	}
	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect for pairing: %w", err)
	}

	fmt.Println()
	fmt.Println("Open WhatsApp on your phone: Settings > Linked Devices > Link a Device.")
	fmt.Println("Scan this QR code (it refreshes automatically):")
	fmt.Println()

	timeout := 90 * time.Second
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("QR pairing timed out, please try again")
		case item, ok := <-qrChan:
			if !ok {
				return errors.New("QR channel closed before pairing completed")
			}
			switch item.Event {
			case "code":
				printQRToTerminal(os.Stdout, item.Code)
			case whatsmeow.QRChannelSuccess.Event:
				fmt.Println("Pairing successful.")
				return waitForLogin(ctx, client, 30*time.Second)
			case whatsmeow.QRChannelTimeout.Event:
				return errors.New("QR pairing timed out, please try again")
			case whatsmeow.QRChannelClientOutdated.Event:
				return errors.New("client outdated, update whatsmeow and retry")
			case whatsmeow.QRChannelScannedWithoutMultidevice.Event:
				return errors.New("multi-device is disabled on the phone, enable it and retry")
			default:
				if item.Error != nil {
					return fmt.Errorf("pairing error: %w", item.Error)
				}
			}
		}
	}
}

// pairWithPhone requests a pairing code for the given phone number (companion
// mode without scanning a QR code).
func pairWithPhone(ctx context.Context, client *whatsmeow.Client, logger *slog.Logger, phone string) error {
	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect for pairing: %w", err)
	}
	code, err := client.PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		return fmt.Errorf("request pairing code: %w", err)
	}
	fmt.Println()
	fmt.Println("Open WhatsApp on your phone: Settings > Linked Devices > Link to Existing Account.")
	fmt.Printf("Enter this code there: %s\n", code)
	fmt.Println()
	logger.Info("Waiting for pairing to complete with the entered code...")

	if err := waitForPairAndLogin(ctx, client, 3*time.Minute); err != nil {
		return err
	}
	return nil
}

func waitForLogin(ctx context.Context, client *whatsmeow.Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if client.IsConnected() && client.IsLoggedIn() {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for WhatsApp login")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func waitForPairAndLogin(ctx context.Context, client *whatsmeow.Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if client.IsConnected() && client.IsLoggedIn() {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for pairing code confirmation")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}