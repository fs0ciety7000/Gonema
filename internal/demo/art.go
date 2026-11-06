package demo

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"strings"
)

// glyphs is a 5x7 bitmap font (bit 4 = leftmost pixel) for the characters of
// generated titles. Unknown characters render as a space.
var glyphs = map[rune][7]uint8{
	'A': {0x0e, 0x11, 0x11, 0x1f, 0x11, 0x11, 0x11}, 'B': {0x1e, 0x11, 0x11, 0x1e, 0x11, 0x11, 0x1e},
	'C': {0x0e, 0x11, 0x10, 0x10, 0x10, 0x11, 0x0e}, 'D': {0x1e, 0x11, 0x11, 0x11, 0x11, 0x11, 0x1e},
	'E': {0x1f, 0x10, 0x10, 0x1e, 0x10, 0x10, 0x1f}, 'F': {0x1f, 0x10, 0x10, 0x1e, 0x10, 0x10, 0x10},
	'G': {0x0e, 0x11, 0x10, 0x17, 0x11, 0x11, 0x0f}, 'H': {0x11, 0x11, 0x11, 0x1f, 0x11, 0x11, 0x11},
	'I': {0x0e, 0x04, 0x04, 0x04, 0x04, 0x04, 0x0e}, 'J': {0x07, 0x02, 0x02, 0x02, 0x02, 0x12, 0x0c},
	'K': {0x11, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11}, 'L': {0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1f},
	'M': {0x11, 0x1b, 0x15, 0x15, 0x11, 0x11, 0x11}, 'N': {0x11, 0x11, 0x19, 0x15, 0x13, 0x11, 0x11},
	'O': {0x0e, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0e}, 'P': {0x1e, 0x11, 0x11, 0x1e, 0x10, 0x10, 0x10},
	'Q': {0x0e, 0x11, 0x11, 0x11, 0x15, 0x12, 0x0d}, 'R': {0x1e, 0x11, 0x11, 0x1e, 0x14, 0x12, 0x11},
	'S': {0x0f, 0x10, 0x10, 0x0e, 0x01, 0x01, 0x1e}, 'T': {0x1f, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04},
	'U': {0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0e}, 'V': {0x11, 0x11, 0x11, 0x11, 0x11, 0x0a, 0x04},
	'W': {0x11, 0x11, 0x11, 0x15, 0x15, 0x15, 0x0a}, 'X': {0x11, 0x11, 0x0a, 0x04, 0x0a, 0x11, 0x11},
	'Y': {0x11, 0x11, 0x0a, 0x04, 0x04, 0x04, 0x04}, 'Z': {0x1f, 0x01, 0x02, 0x04, 0x08, 0x10, 0x1f},
	'0': {0x0e, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0e}, '1': {0x04, 0x0c, 0x04, 0x04, 0x04, 0x04, 0x0e},
	'2': {0x0e, 0x11, 0x01, 0x02, 0x04, 0x08, 0x1f}, '3': {0x1f, 0x02, 0x04, 0x02, 0x01, 0x11, 0x0e},
	'4': {0x02, 0x06, 0x0a, 0x12, 0x1f, 0x02, 0x02}, '5': {0x1f, 0x10, 0x1e, 0x01, 0x01, 0x11, 0x0e},
	'6': {0x06, 0x08, 0x10, 0x1e, 0x11, 0x11, 0x0e}, '7': {0x1f, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	'8': {0x0e, 0x11, 0x11, 0x0e, 0x11, 0x11, 0x0e}, '9': {0x0e, 0x11, 0x11, 0x0f, 0x01, 0x02, 0x0c},
	'-': {0, 0, 0, 0x1f, 0, 0, 0}, ':': {0, 0x0c, 0x0c, 0, 0x0c, 0x0c, 0},
	'.': {0, 0, 0, 0, 0, 0x0c, 0x0c}, '\'': {0x04, 0x04, 0x08, 0, 0, 0, 0},
	'&': {0x0c, 0x12, 0x14, 0x08, 0x15, 0x12, 0x0d},
}

// Art describes a generated image.
type Art struct {
	Title string
	Year  int64
	Hue   float64 // 0-360
}

// hsl converts HSL (h in degrees, s and l in 0..1) to RGB.
func hsl(h, s, l float64) color.RGBA {
	c := (1 - math.Abs(2*l-1)) * s
	hp := math.Mod(h, 360) / 60
	x := c * (1 - math.Abs(math.Mod(hp, 2)-1))
	var r, g, b float64
	switch {
	case hp < 1:
		r, g = c, x
	case hp < 2:
		r, g = x, c
	case hp < 3:
		g, b = c, x
	case hp < 4:
		g, b = x, c
	case hp < 5:
		r, b = x, c
	default:
		r, b = c, x
	}
	m := l - c/2
	to8 := func(v float64) uint8 { return uint8(math.Round(math.Max(0, math.Min(1, v+m)) * 255)) }
	return color.RGBA{to8(r), to8(g), to8(b), 255}
}

// DominantColor is the "#rrggbb" color stored for an image of hue h: the main
// tone of the gradient, as the image cache would extract it.
func DominantColor(h float64) string {
	c := hsl(h, 0.55, 0.42)
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// Render draws a w x h image: a diagonal gradient in the hue, a soft light
// band, and the title (word-wrapped) with the year near the bottom. It is
// deterministic. Output is JPEG.
func Render(a Art, w, h int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	top := hsl(a.Hue, 0.55, 0.42)
	bottom := hsl(a.Hue+38, 0.6, 0.10)
	for y := range h {
		row := img.Pix[y*img.Stride:]
		for x := range w {
			t := (float64(y)/float64(h))*0.8 + (float64(x)/float64(w))*0.2
			// a soft diagonal band of light
			band := math.Max(0, 1-math.Abs(float64(x)/float64(w)-float64(y)/float64(h)*0.7-0.15)*5) * 0.12
			mix := func(p, q uint8) uint8 {
				v := float64(p)*(1-t) + float64(q)*t + band*255
				return uint8(math.Min(255, v))
			}
			i := x * 4
			row[i], row[i+1], row[i+2], row[i+3] = mix(top.R, bottom.R), mix(top.G, bottom.G), mix(top.B, bottom.B), 255
		}
	}
	// Title: as large as the width allows for the longest line (12 to 16 cells).
	lines := wrap(strings.ToUpper(a.Title), 12)
	longest := 1
	for _, l := range lines {
		longest = max(longest, len([]rune(l)))
	}
	scale := max(1, (w*8/10)/(longest*6))
	scale = min(scale, max(1, w/(12*6)))
	lineH := 9 * scale
	yearScale := max(1, scale/2)
	blockH := len(lines)*lineH + 10*yearScale
	y0 := h - h/10 - blockH
	if h < w { // backdrop: vertically centered on the left third
		y0 = (h - blockH) / 2
	}
	x0 := w / 12
	ink := color.RGBA{0xf4, 0xf1, 0xea, 0xff}
	for i, l := range lines {
		drawText(img, l, x0, y0+i*lineH, scale, ink)
	}
	if a.Year > 0 {
		drawText(img, fmt.Sprint(a.Year), x0, y0+len(lines)*lineH+2*yearScale, yearScale, color.RGBA{0xd8, 0xd2, 0xc4, 0xff})
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		return nil, fmt.Errorf("demo: encode image: %w", err)
	}
	return buf.Bytes(), nil
}

// wrap splits s into lines of at most n characters, on spaces when possible.
func wrap(s string, n int) []string {
	var lines []string
	var cur string
	for word := range strings.FieldsSeq(s) {
		switch {
		case cur == "":
			cur = word
		case len([]rune(cur))+1+len([]rune(word)) <= n:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) > 4 {
		lines = lines[:4]
	}
	return lines
}

func drawText(img *image.RGBA, s string, x0, y0, scale int, c color.RGBA) {
	b := img.Bounds()
	x := x0
	for _, r := range s {
		g := glyphs[r]
		for row := range 7 {
			bits := g[row]
			for col := range 5 {
				if bits&(1<<(4-col)) == 0 {
					continue
				}
				for dy := range scale {
					py := y0 + row*scale + dy
					if py < b.Min.Y || py >= b.Max.Y {
						continue
					}
					for dx := range scale {
						px := x + col*scale + dx
						if px < b.Min.X || px >= b.Max.X {
							continue
						}
						i := img.PixOffset(px, py)
						img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
					}
				}
			}
		}
		x += 6 * scale
	}
}
