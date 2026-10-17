package link

import (
	"io"

	"github.com/mdp/qrterminal/v3"
	"rsc.io/qr"
)

// printQR draws text as a QR code made of half-block characters (two QR rows
// per terminal line), so it fits a normal terminal window.
func printQR(w io.Writer, text string) {
	qrterminal.GenerateHalfBlock(text, qr.L, w)
}
