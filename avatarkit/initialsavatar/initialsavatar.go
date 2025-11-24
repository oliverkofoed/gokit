package initialsavatar

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"os"
	"strings"
	"sync"

	"github.com/flopp/go-findfont"
	"github.com/fogleman/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
)

type generator struct {
	fontNames []string

	fontOnce sync.Once
	font     *opentype.Font
}

var DefaultSansSerifStack = []string{"Helvetica Neue", "Helvetica", "Arial", "Nimbus Sans", "Liberation Sans", "sans", "sans-serif"}

// NewGenerator returns an avatar generator that draws the seed text centered on a colored background.
// fontNames are tried in order until a matching system font is found (using go-findfont). If none are
// found, it falls back to the bundled Go Regular font. Passing nil or an empty slice uses DefaultSansSerifStack.
func NewGenerator(fontNames []string) *generator {
	if len(fontNames) == 0 {
		fontNames = DefaultSansSerifStack
	}
	return &generator{
		fontNames: fontNames,
	}
}

// Generate returns a PNG image as bytes.
func (g *generator) Generate(seed string) []byte {
	if seed == "" {
		seed = "?"
	}

	baseFont := g.ensureFont()

	rnd := rand.New(rand.NewSource(int64(hashSeed(seed))))
	size := 200
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	dc := gg.NewContextForRGBA(img)

	// background
	bg := backgroundColors[rnd.Intn(len(backgroundColors))].unit()
	dc.SetRGB(bg.r, bg.g, bg.b)
	dc.Clear()

	// choose font size to fit inside the avatar with some margin
	margin := float64(size) * 0.1
	maxWidth := float64(size) - 2*margin
	maxHeight := float64(size) - 2*margin

	fontSize := float64(size) * 0.65
	minFontSize := float64(size) * 0.2

	var face font.Face
	for {
		var err error
		face, err = opentype.NewFace(baseFont, &opentype.FaceOptions{
			Size:    fontSize,
			DPI:     72,
			Hinting: font.HintingNone,
		})
		if err != nil {
			panic(err)
		}
		dc.SetFontFace(face)

		w, h := dc.MeasureString(seed)
		if w <= maxWidth && h <= maxHeight {
			break
		}

		fontSize *= 0.9
		if fontSize < minFontSize {
			// give up shrinking further; just use what we have
			break
		}
	}

	// text color & *fully* centered text (both horizontally & vertically).
	// gg's DrawStringAnchored uses approximated height; instead center using font bounds.
	dc.SetColor(textColor(bg))
	bounds, _ := font.BoundString(face, seed)
	centerX := float64(size) / 2
	centerY := float64(size) / 2
	// BoundString returns 26.6 fixed numbers; convert and offset so bounding box center matches avatar center.
	x := centerX - float64(bounds.Min.X+bounds.Max.X)/(2 * 64)
	y := centerY - float64(bounds.Min.Y+bounds.Max.Y)/(2 * 64)
	dc.DrawString(seed, x, y)

	// encode PNG
	buf := bytes.NewBuffer(nil)
	enc := png.Encoder{}
	if err := enc.Encode(buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// ensureFont lazy-loads the font the first time Generate is called.
// It tries system fonts via go-findfont; on failure it falls back to
// goregular.TTF and prints a warning to stderr.
func (g *generator) ensureFont() *opentype.Font {
	g.fontOnce.Do(func() {
		for _, name := range g.fontNames {
			n := strings.TrimSpace(name)
			if n == "" {
				continue
			}

			if path, err := findfont.Find(n); err == nil {
				data, err := os.ReadFile(path)
				if err != nil {
					fmt.Fprintf(os.Stderr, "initialsavatar: failed to read system font %q: %v\n", path, err)
					continue
				}
				f, err := opentype.Parse(data)
				if err != nil {
					fmt.Fprintf(os.Stderr, "initialsavatar: failed to parse system font %q: %v\n", path, err)
					continue
				}
				g.font = f
				return
			}
		}

		// Fallback to bundled Go Regular.
		fallback, err := opentype.Parse(goregular.TTF)
		if err != nil {
			// This really shouldn't happen; panic is reasonable here.
			panic(fmt.Errorf("initialsavatar: failed to parse bundled Go Regular font: %w", err))
		}

		if len(g.fontNames) > 0 {
			fmt.Fprintf(os.Stderr, "initialsavatar: falling back to bundled Go Regular font after checking %v\n", g.fontNames)
		}

		g.font = fallback
	})

	return g.font
}

func hashSeed(seed string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	return h.Sum64()
}

type rgb struct {
	r float64
	g float64
	b float64
}

func (c rgb) unit() rgb {
	return rgb{r: c.r / 255, g: c.g / 255, b: c.b / 255}
}

func textColor(bg rgb) color.Color {
	l := 0.2126*bg.r + 0.7152*bg.g + 0.0722*bg.b
	if l > 0.7 {
		return color.RGBA{R: 20, G: 20, B: 20, A: 255}
	}
	// small tint to avoid full black on dark backgrounds
	return color.RGBA{R: 250, G: 250, B: 250, A: 255}
}

var backgroundColors = []rgb{
	{0xFF, 0xFF, 0xFF},
	{0x23, 0x20, 0x48},
	{0x20, 0x43, 0x48},
	{0x20, 0x48, 0x2B},
	{0x47, 0x48, 0x20},
	{0x48, 0x2D, 0x20},
	{0x23, 0x20, 0x48},
	{0x48, 0x20, 0x45},
	{0x48, 0x20, 0x2B},
	{0xEE, 0xDC, 0xD5},
	{0xEE, 0xE8, 0xD5},
	{0xE8, 0xEE, 0xD5},
	{0xD5, 0xEE, 0xD5},
	{0xD5, 0xEE, 0xEE},
	{0xD5, 0xE3, 0xEE},
	{0xDA, 0xD5, 0xEE},
	{0xE7, 0xD5, 0xEE},
	{0xEE, 0xD5, 0xE7},
	{0xEE, 0xD5, 0xDB},
}
