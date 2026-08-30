// QR rendering for the terminal: shows the WhatsApp pairing code as a real,
// scannable ASCII QR code so the user does not need an external generator.
package main

import (
	"fmt"
	"io"

	qrcode "github.com/skip2/go-qrcode"
)

// printQRToTerminal renders the raw pairing payload as a block-character QR
// code with a quiet zone, plus the raw payload as a fallback.
func printQRToTerminal(w io.Writer, payload string) {
	qr, err := qrcode.New(payload, qrcode.Low)
	if err != nil {
		fmt.Fprintf(w, "Could not render QR code: %v\nRaw payload:\n%s\n", err, payload)
		return
	}
	bmp := qr.Bitmap() // true = dark module
	fmt.Fprintln(w)
	// Invert: we print a white (default terminal) background with dark blocks.
	// Border of 2 modules on each side for the quiet zone.
	border := 2
	width := len(bmp[0]) + border*2
	blank := make([]byte, 0, width*2)
	for i := 0; i < width; i++ {
		blank = append(blank, []byte("██")...)
	}
	for i := 0; i < border; i++ {
		fmt.Fprintln(w, string(blank))
	}
	for _, row := range bmp {
		line := make([]byte, 0, width*2)
		for i := 0; i < border; i++ {
			line = append(line, []byte("██")...)
		}
		for _, px := range row {
			// Terminal default background is dark: print light blocks for dark
			// modules and leave dark background for light modules.
			if px {
				line = append(line, []byte("  ")...)
			} else {
				line = append(line, []byte("██")...)
			}
		}
		for i := 0; i < border; i++ {
			line = append(line, []byte("██")...)
		}
		fmt.Fprintln(w, string(line))
	}
	for i := 0; i < border; i++ {
		fmt.Fprintln(w, string(blank))
	}
	fmt.Fprintf(w, "If the QR code does not scan, paste this payload into any QR generator:\n%s\n\n", payload)
}