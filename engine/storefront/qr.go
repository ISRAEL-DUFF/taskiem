package storefront

import (
	"fmt"
	"strings"

	"rsc.io/qr"
)

// The storefront's address as a QR code (docs/storefronts.md#qr-code-and-printable-card):
// the web app shows and prints it, the public host serves it as
// /qr.png, and the chat sends that image. One encoding for all three.

// QRScale is the size of a module in the PNG, in pixels.
const QRScale = 12

// QR encodes an address at medium error correction.
func QR(address string) (*qr.Code, error) {
	return qr.Encode(address, qr.M)
}

// QRPNG is an address's QR code as a PNG.
func QRPNG(address string) ([]byte, error) {
	code, err := QR(address)
	if err != nil {
		return nil, err
	}
	code.Scale = QRScale
	return code.PNG(), nil
}

// QRSVG is an address's QR code as SVG, with a four-module quiet zone.
func QRSVG(address string) (string, error) {
	c, err := QR(address)
	if err != nil {
		return "", err
	}
	n := c.Size + 8
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n, n, n)
	for y := range c.Size {
		for x := range c.Size {
			if c.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+4, y+4)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}

// PreviewURL is where a published storefront serves on the preview
// domain, or "" without one.
func PreviewURL(publishedSlug *string, previewDomain string) string {
	if previewDomain == "" || publishedSlug == nil || *publishedSlug == "" {
		return ""
	}
	return "https://" + *publishedSlug + "." + previewDomain
}
