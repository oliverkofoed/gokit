package emojiavatar

import (
	"bytes"
	"hash/fnv"
	"image"
	"image/png"
	"io/fs"
	"math/rand"
	"path/filepath"
	"strings"

	"github.com/fogleman/gg"
)

type generator struct {
	overlayFS     fs.FS
	overlayImages []string
}

// NewGenerator returns a deterministic PNG generator backed by the provided
// filesystem (or the embedded emoji set when nil).
func NewGenerator(files fs.FS) *generator {
	if files == nil {
		panic("must supply files")
	}

	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		panic(err)
	}

	overlayImages := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".png") {
			overlayImages = append(overlayImages, entry.Name())
		}
	}

	if len(overlayImages) == 0 {
		panic("emojiavatar: no overlay images found")
	}

	return &generator{
		overlayFS:     files,
		overlayImages: overlayImages,
	}
}

func (g *generator) Generate(seed string) []byte {
	if seed == "" {
		seed = "default"
	}

	rnd := rand.New(rand.NewSource(int64(hashSeed(seed))))
	size := 200
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	dc := gg.NewContextForRGBA(img)
	bg := backgroundColors[rnd.Intn(len(backgroundColors))].unit()
	dc.SetRGB(bg.r, bg.g, bg.b)
	dc.DrawRectangle(-10, -10, float64(size)+20, float64(size)+20)
	dc.Fill()

	reader, err := g.overlayFS.Open(g.overlayImages[rnd.Intn(len(g.overlayImages))])
	if err != nil {
		panic(err)
	}
	defer reader.Close()
	e, _, err := image.Decode(reader)
	if err != nil {
		panic(err)
	}
	dc.DrawImage(e, 20, 20)

	buf := bytes.NewBuffer(nil)
	enc := png.Encoder{}
	err = enc.Encode(buf, img)
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func hashSeed(seed string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	return h.Sum64()
}

type color struct {
	r float64
	g float64
	b float64
}

func (c color) unit() color {
	return color{r: c.r / 255, g: c.g / 255, b: c.b / 255}
}

var backgroundColors = []color{
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
