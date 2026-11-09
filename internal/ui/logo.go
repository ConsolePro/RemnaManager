package ui

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

//go:embed assets/logo.png
var logoPNG []byte
var logoOnce sync.Once
var logoLarge, logoSmall string

// Render the embedded PNG using half-block cells. No terminal graphics protocol
// or remote file is needed; Lip Gloss reduces colors to the terminal's profile.
func terminalLogo(compact bool) string {
	if os.Getenv("NO_COLOR") != "" {
		if compact {
			return "╭──╮\n╰R›╯"
		}
		return "╭──╮\n│R›│\n╰──╯"
	}
	logoOnce.Do(func() {
		img, _, err := image.Decode(bytes.NewReader(logoPNG))
		if err != nil {
			logoLarge = "◆"
			logoSmall = "◆"
			return
		}
		bounds := markBounds(img)
		logoLarge = renderRaster(img, bounds, 12, 6)
		logoSmall = renderRaster(img, bounds, 4, 2)
	})
	if compact {
		return logoSmall
	}
	return logoLarge
}

func markBounds(img image.Image) image.Rectangle {
	bounds := img.Bounds()
	mark := image.Rect(bounds.Max.X, bounds.Max.Y, 0, 0)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if g > 20000 && g > r*6/5 && g > b*11/10 {
				mark.Min.X = min(mark.Min.X, x)
				mark.Min.Y = min(mark.Min.Y, y)
				mark.Max.X = max(mark.Max.X, x+1)
				mark.Max.Y = max(mark.Max.Y, y+1)
			}
		}
	}
	if mark.Empty() {
		return bounds
	}
	return mark.Inset(-max(1, mark.Dx()/12)).Intersect(bounds)
}

func renderRaster(img image.Image, bounds image.Rectangle, columns, rows int) string {
	colorAt := func(x, y int) string {
		x0, x1 := bounds.Min.X+x*bounds.Dx()/columns, bounds.Min.X+(x+1)*bounds.Dx()/columns
		y0, y1 := bounds.Min.Y+y*bounds.Dy()/(rows*2), bounds.Min.Y+(y+1)*bounds.Dy()/(rows*2)
		var r, g, b, n uint64
		for py := y0; py < y1; py++ {
			for px := x0; px < x1; px++ {
				rr, gg, bb, _ := img.At(px, py).RGBA()
				r += uint64(rr >> 8)
				g += uint64(gg >> 8)
				b += uint64(bb >> 8)
				n++
			}
		}
		if n == 0 {
			return background
		}
		return fmt.Sprintf("#%02x%02x%02x", r/n, g/n, b/n)
	}
	lines := make([]string, rows)
	for y := 0; y < rows; y++ {
		var line strings.Builder
		for x := 0; x < columns; x++ {
			line.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(colorAt(x, 2*y))).Background(lipgloss.Color(colorAt(x, 2*y+1))).Render("▀"))
		}
		lines[y] = line.String()
	}
	return strings.Join(lines, "\n")
}
