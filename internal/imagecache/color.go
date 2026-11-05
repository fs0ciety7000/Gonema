package imagecache

import (
	"fmt"
	"image"
	"image/color"
	"math"
)

// Tuning of DominantColor. Lightness and chroma are OKLab values (L in 0..1,
// chroma about 0..0.32 for sRGB).
const (
	sampleSide   = 64   // the longest side is sampled at most this many times
	minLightness = 0.22 // darker pixels are "near black" and ignored
	maxLightness = 0.95 // lighter pixels are "near white" and ignored
	minChroma    = 0.04 // less saturated pixels are "greyish" and ignored
	minShare     = 0.03 // a hue cluster must cover at least 3 % of the sampled pixels
	hueBins      = 36   // 10° per bin
	alphaCutoff  = 0x80 // pixels more transparent than this are ignored
)

// lab is a colour in OKLab (https://bottosson.github.io/posts/oklab/).
type lab struct{ L, A, B float64 }

// srgbToLinear maps an 8-bit sRGB channel to linear light.
var srgbToLinear = func() (t [256]float64) {
	for i := range t {
		c := float64(i) / 255
		if c <= 0.04045 {
			t[i] = c / 12.92
		} else {
			t[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return t
}()

func toLab(r, g, b uint8) lab {
	lr, lg, lb := srgbToLinear[r], srgbToLinear[g], srgbToLinear[b]
	l := math.Cbrt(0.4122214708*lr + 0.5363325363*lg + 0.0514459929*lb)
	m := math.Cbrt(0.2119034982*lr + 0.6806995451*lg + 0.1073969566*lb)
	s := math.Cbrt(0.0883024619*lr + 0.2817188376*lg + 0.6299787005*lb)
	return lab{
		L: 0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		A: 1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		B: 0.0259040371*l + 0.7827717662*m - 0.8086757660*s,
	}
}

func (c lab) hex() string {
	l := c.L + 0.3963377774*c.A + 0.2158037573*c.B
	m := c.L - 0.1055613458*c.A - 0.0638541728*c.B
	s := c.L - 0.0894841775*c.A - 1.2914855480*c.B
	l, m, s = l*l*l, m*m*m, s*s*s
	r := 4.0767416621*l - 3.3077115913*m + 0.2309699292*s
	g := -1.2684380046*l + 2.6097574011*m - 0.3413193965*s
	b := -0.0041960863*l - 0.7034186147*m + 1.7076147010*s
	return fmt.Sprintf("#%02x%02x%02x", encode8(r), encode8(g), encode8(b))
}

// encode8 converts linear light to an 8-bit sRGB channel, clamped.
func encode8(c float64) uint8 {
	switch {
	case c <= 0:
		return 0
	case c >= 1:
		return 255
	case c <= 0.0031308:
		c *= 12.92
	default:
		c = 1.055*math.Pow(c, 1/2.4) - 0.055
	}
	return uint8(math.Round(c * 255))
}

// DominantColor returns the characteristic colour of img as "#rrggbb", or "" for
// an empty image. It is pure and deterministic.
//
// Algorithm:
//  1. Sample a grid of at most 64 pixels on the longest side (nearest pixel).
//  2. Convert to OKLab, a perceptual space where distances and averages behave.
//  3. Ignore transparent, near-black (L < 0.22), near-white (L > 0.95) and greyish
//     (chroma < 0.04) pixels for the hue analysis.
//  4. Build a 36-bin hue histogram where each pixel weighs chroma²: vivid pixels
//     count much more than dull ones, so a red title on a dark poster beats a
//     larger, faintly tinted background.
//  5. Smooth the histogram over neighbouring bins and pick the strongest window
//     among those covering at least 3 % of the sampled pixels (a few vivid pixels
//     of noise cannot win). Ties go to the lowest hue.
//  6. Return the chroma²-weighted OKLab mean of that window.
//
// Without such a window (greyscale or almost-black posters) it falls back to the
// mean colour of all sampled pixels. The UI derives the movie tint from the hue
// of this colour and caps chroma and lightness itself (DESIGN.md, "Teinte film").
func DominantColor(img image.Image) string {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return ""
	}
	step := (max(w, h) + sampleSide - 1) / sampleSide
	// Sample the centre of each step×step cell.
	off := step / 2

	var (
		total  int
		sum    lab
		binW   [hueBins]float64
		binN   [hueBins]int
		binSum [hueBins]lab
		px     = pixelReader(img)
	)
	for y := b.Min.Y + min(off, h-1); y < b.Max.Y; y += step {
		for x := b.Min.X + min(off, w-1); x < b.Max.X; x += step {
			r, g, bl, a := px(x, y)
			if a < alphaCutoff {
				continue
			}
			c := toLab(r, g, bl)
			total++
			sum.L += c.L
			sum.A += c.A
			sum.B += c.B
			if c.L < minLightness || c.L > maxLightness {
				continue
			}
			chroma2 := c.A*c.A + c.B*c.B
			if chroma2 < minChroma*minChroma {
				continue
			}
			hue := math.Atan2(c.B, c.A) // -π..π
			bin := int((hue + math.Pi) / (2 * math.Pi) * hueBins)
			if bin >= hueBins {
				bin = hueBins - 1
			}
			binW[bin] += chroma2
			binN[bin]++
			binSum[bin].L += c.L * chroma2
			binSum[bin].A += c.A * chroma2
			binSum[bin].B += c.B * chroma2
		}
	}
	if total == 0 {
		return ""
	}

	best, bestScore := -1, 0.0
	for i := range hueBins {
		p, n := (i+hueBins-1)%hueBins, (i+1)%hueBins
		count := binN[p] + binN[i] + binN[n]
		if float64(count) < minShare*float64(total) {
			continue
		}
		if score := binW[p] + 2*binW[i] + binW[n]; score > bestScore {
			best, bestScore = i, score
		}
	}
	if best < 0 {
		t := float64(total)
		return lab{sum.L / t, sum.A / t, sum.B / t}.hex()
	}
	var acc lab
	var wsum float64
	for _, i := range [...]int{(best + hueBins - 1) % hueBins, best, (best + 1) % hueBins} {
		acc.L += binSum[i].L
		acc.A += binSum[i].A
		acc.B += binSum[i].B
		wsum += binW[i]
	}
	return lab{acc.L / wsum, acc.A / wsum, acc.B / wsum}.hex()
}

// pixelReader returns a fast 8-bit RGBA accessor for the common decoded types.
func pixelReader(img image.Image) func(x, y int) (r, g, b, a uint8) {
	switch m := img.(type) {
	case *image.YCbCr: // JPEG
		return func(x, y int) (uint8, uint8, uint8, uint8) {
			yi, ci := m.YOffset(x, y), m.COffset(x, y)
			r, g, b := color.YCbCrToRGB(m.Y[yi], m.Cb[ci], m.Cr[ci])
			return r, g, b, 0xff
		}
	case *image.RGBA: // premultiplied; un-premultiply for partially transparent pixels
		return func(x, y int) (uint8, uint8, uint8, uint8) {
			i := m.PixOffset(x, y)
			p := m.Pix[i : i+4 : i+4]
			return unpremultiply(p[0], p[1], p[2], p[3])
		}
	case *image.NRGBA: // PNG
		return func(x, y int) (uint8, uint8, uint8, uint8) {
			i := m.PixOffset(x, y)
			p := m.Pix[i : i+4 : i+4]
			return p[0], p[1], p[2], p[3]
		}
	default:
		return func(x, y int) (uint8, uint8, uint8, uint8) {
			r, g, b, a := img.At(x, y).RGBA()
			return unpremultiply(uint8(r>>8), uint8(g>>8), uint8(b>>8), uint8(a>>8))
		}
	}
}

func unpremultiply(r, g, b, a uint8) (uint8, uint8, uint8, uint8) {
	if a == 0xff || a == 0 {
		return r, g, b, a
	}
	f := func(c uint8) uint8 { return uint8(min(255, uint32(c)*255/uint32(a))) }
	return f(r), f(g), f(b), a
}
