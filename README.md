# aboutmeow

Keeps a permanent WhatsApp "About" (bio) alive. WhatsApp shows the About text
only for a limited time (like a Discord status, it disappears after max
~30 days). aboutmeow pairs with your account via whatsmeow and re-sets the
text automatically: right after connecting, after every reconnect, and on a
daily renewal cycle. Your bio stays visible permanently.

Built on [whatsmeow](https://github.com/tulir/whatsmeow) (WhatsApp Web multidevice API, Go).

## How it works

- First run performs an interactive login (QR code in the terminal, or a
  phone-number pairing code with `-phone`).
- The session is stored in a SQLite database, so later runs connect without
  scanning again.
- After connecting, the configured text is pushed via
  `Client.SetStatusMessage` (the official "update text status" mutation).
- With `-daemon` the app stays connected, follows reconnects and renews the
  bio every 24 hours, so the 30-day expiry never kicks in.

Note: the phone must stay linked to the WhatsApp account (it appears as a
"linked device", like WhatsApp Web). If you unlink it or log out remotely,
aboutmeow stops; delete the db file and pair again.

## Build

Requires Go 1.26+.

    go build -o aboutmeow .

## Usage

    # 1. First run, pair by scanning the QR code with your phone
    ./aboutmeow -bio "Your permanent bio text." -daemon

    # Alternative: pair with a code instead of a QR scan
    ./aboutmeow -bio "Your permanent bio text." -daemon -phone 491234567890

    # One-shot: set the bio once and exit (no auto renewal)
    ./aboutmeow -bio "Your permanent bio text."

    # Change the bio later: just run again with the new text
    ./aboutmeow -bio "New text." -daemon

### Flags

- `-bio` (required): the text to keep alive.
- `-daemon`: keep running and renew daily (otherwise exit after the first update).
- `-bio-file`: path to a UTF-8 text file containing the bio (recommended for systemd and multi-line/emoji bios).
- `-duration`: expiry in seconds (e.g. `86400` = 1 day). Default `0` = WhatsApp's server default (~30 days).
- `-phone`: international format phone number for code-based pairing.
- `-db`: path to the SQLite session store (default `~/.local/share/aboutmeow/session.db`).
- `-log`: `debug`, `info`, `warn` or `error`.

## Run as a systemd service

    sudo cp aboutmeow /usr/local/bin/
    # edit ExecStart in aboutmeow.service (set your bio text)
    sudo cp aboutmeow.service /etc/systemd/system/
    sudo systemctl daemon-reload
    sudo systemctl enable --now aboutmeow

    # first pairing: run once interactively with the same -db path
    sudo mkdir -p /var/lib/aboutmeow
    sudo aboutmeow -bio "..." -db /var/lib/aboutmeow/session.db
    sudo systemctl start aboutmeow

## Risks

WhatsApp does not officially support unofficial clients; using one can in the
worst case get the number banned. Use at your own risk, ideally not on your
primary number.