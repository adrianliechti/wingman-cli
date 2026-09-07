package remote

import (
	"strings"

	"github.com/skip2/go-qrcode"
)

// QRCode renders content as a terminal QR code using half-block characters.
func QRCode(content string) string {
	q, err := qrcode.New(content, qrcode.Low)
	if err != nil {
		return ""
	}
	bitmap := q.Bitmap()
	var b strings.Builder
	for y := 0; y < len(bitmap); y += 2 {
		for x := range bitmap[y] {
			top := bitmap[y][x]
			bottom := y+1 < len(bitmap) && bitmap[y+1][x]
			switch {
			case top && bottom:
				b.WriteString(" ")
			case top:
				b.WriteString("▄")
			case bottom:
				b.WriteString("▀")
			default:
				b.WriteString("█")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}
