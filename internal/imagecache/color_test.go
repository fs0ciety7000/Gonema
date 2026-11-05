package imagecache

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"strconv"
	"testing"
)

func hexRGB(t *testing.T, s string) (r, g, b uint8) {
	t.Helper()
	if len(s) != 7 || s[0] != '#' {
		t.Fatalf("not #rrggbb: %q", s)
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return uint8(v >> 16), uint8(v >> 8), uint8(v)
}

func rgb(hex uint32) color.NRGBA {
	return color.NRGBA{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), A: 0xff}
}

// hueDistance is the OKLab hue angle between two colours, in degrees.
func hueDistance(t *testing.T, got string, want color.NRGBA) float64 {
	t.Helper()
	r, g, b := hexRGB(t, got)
	a, w := toLab(r, g, b), toLab(want.R, want.G, want.B)
	d := math.Abs(math.Atan2(a.B, a.A)-math.Atan2(w.B, w.A)) * 180 / math.Pi
	if d > 180 {
		d = 360 - d
	}
	return d
}

// poster builds a 342×513 image: background, plus each accent as a horizontal
// band covering the given share of the height.
func poster(bg color.NRGBA, accents ...struct {
	c     color.NRGBA
	share float64
}) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 342, 513))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)
	y := 0
	for _, a := range accents {
		h := int(math.Round(a.share * 513))
		draw.Draw(img, image.Rect(0, y, 342, y+h), &image.Uniform{C: a.c}, image.Point{}, draw.Src)
		y += h
	}
	return img
}

type accent = struct {
	c     color.NRGBA
	share float64
}

func TestDominantColor(t *testing.T) {
	t.Parallel()
	red, blue, orange := rgb(0xdc2626), rgb(0x1d4ed8), rgb(0xf97316)
	cyan, green, black, white := rgb(0x00c8ff), rgb(0x16a34a), rgb(0x0a0a0a), rgb(0xfafafa)
	brown := rgb(0x8f725a) // dull warm brown, OKLab chroma 0.051 (above the 0.04 floor)

	tests := []struct {
		name string
		img  image.Image
		want color.NRGBA
		// exact: the result must be within 2 per channel of want (not only the hue).
		exact bool
	}{
		{"solid red", poster(red), red, true},
		{"solid blue", poster(blue), blue, true},
		{"two tone, larger area wins at equal vividness", poster(red, accent{blue, 0.75}), blue, false},
		{"two tone reversed", poster(blue, accent{red, 0.75}), red, false},
		{"mostly black with a vivid accent", poster(black, accent{orange, 0.08}), orange, true},
		{"mostly white with a vivid accent", poster(white, accent{green, 0.10}), green, true},
		{"vivid accent beats a large dull area", poster(brown, accent{cyan, 0.15}), cyan, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DominantColor(tt.img)
			if again := DominantColor(tt.img); again != got {
				t.Fatalf("not deterministic: %s then %s", got, again)
			}
			if d := hueDistance(t, got, tt.want); d > 5 {
				t.Errorf("DominantColor = %s, hue %.1f° away from #%02x%02x%02x", got, d, tt.want.R, tt.want.G, tt.want.B)
			}
			if tt.exact {
				r, g, b := hexRGB(t, got)
				if absDiff(r, tt.want.R) > 2 || absDiff(g, tt.want.G) > 2 || absDiff(b, tt.want.B) > 2 {
					t.Errorf("DominantColor = %s, want #%02x%02x%02x", got, tt.want.R, tt.want.G, tt.want.B)
				}
			}
		})
	}
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

func TestDominantColorFallbacks(t *testing.T) {
	t.Parallel()
	t.Run("greyscale gives the mean grey", func(t *testing.T) {
		t.Parallel()
		img := image.NewGray(image.Rect(0, 0, 200, 300))
		for y := range 300 {
			for x := range 200 {
				img.SetGray(x, y, color.Gray{Y: uint8(x * 255 / 199)})
			}
		}
		r, g, b := hexRGB(t, DominantColor(img))
		if absDiff(r, g) > 1 || absDiff(g, b) > 1 || r < 90 || r > 160 {
			t.Errorf("greyscale gradient = #%02x%02x%02x, want a mid grey", r, g, b)
		}
	})
	t.Run("a speck below the minimum share is ignored", func(t *testing.T) {
		t.Parallel()
		got := DominantColor(poster(rgb(0x050505), accent{rgb(0xff0000), 0.015}))
		r, _, _ := hexRGB(t, got)
		if r > 60 {
			t.Errorf("DominantColor = %s, want the dark mean, not the red speck", got)
		}
	})
	t.Run("transparent pixels are ignored", func(t *testing.T) {
		t.Parallel()
		img := poster(rgb(0x1d4ed8))
		draw.Draw(img, image.Rect(0, 0, 342, 400), &image.Uniform{C: color.NRGBA{R: 255, A: 0}}, image.Point{}, draw.Src)
		if got := DominantColor(img); hueDistance(t, got, rgb(0x1d4ed8)) > 5 {
			t.Errorf("DominantColor = %s, want blue", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		if got := DominantColor(image.NewRGBA(image.Rectangle{})); got != "" {
			t.Errorf("empty image = %q", got)
		}
		if got := DominantColor(image.NewNRGBA(image.Rect(0, 0, 4, 4))); got != "" {
			t.Errorf("fully transparent image = %q, want \"\"", got)
		}
	})
	t.Run("offset bounds and 1x1", func(t *testing.T) {
		t.Parallel()
		img := image.NewRGBA(image.Rect(-5, 10, -4, 11))
		img.Set(-5, 10, color.RGBA{R: 0xdc, G: 0x26, B: 0x26, A: 0xff})
		if got := DominantColor(img); got != "#dc2626" {
			t.Errorf("1x1 = %s", got)
		}
	})
}

func TestDominantColorJPEG(t *testing.T) {
	t.Parallel()
	src := poster(rgb(0x0a0a0a), accent{rgb(0xf97316), 0.3})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	got, err := DominantColorOf(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if d := hueDistance(t, got, rgb(0xf97316)); d > 5 {
		t.Errorf("JPEG (YCbCr path) = %s, %.1f° from orange", got, d)
	}
	if _, err := DominantColorOf(bytes.NewReader([]byte("<html>"))); err == nil {
		t.Error("DominantColorOf accepted HTML")
	}
}

func TestLabRoundTrip(t *testing.T) {
	t.Parallel()
	for _, c := range []uint32{0x000000, 0xffffff, 0xdc2626, 0x1d4ed8, 0x16a34a, 0x808080, 0x00c8ff} {
		want := "#" + strconv.FormatUint(uint64(c)|1<<24, 16)[1:]
		if got := toLab(uint8(c>>16), uint8(c>>8), uint8(c)).hex(); got != want {
			t.Errorf("round trip %s = %s", want, got)
		}
	}
}

// BenchmarkDominantColor measures a decoded w342 JPEG poster (342×513, YCbCr);
// the budget is 5 ms.
func BenchmarkDominantColor(b *testing.B) {
	src := image.NewNRGBA(image.Rect(0, 0, 342, 513))
	for y := range 513 {
		for x := range 342 {
			src.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 3 / 4), G: uint8(y / 3), B: uint8((x + y) / 4), A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 85}); err != nil {
		b.Fatal(err)
	}
	img, err := jpeg.Decode(&buf)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = DominantColor(img)
	}
	if ns := b.Elapsed().Nanoseconds() / int64(b.N); ns > 5_000_000 {
		b.Errorf("DominantColor took %d ns/op, budget 5 ms", ns)
	}
}
