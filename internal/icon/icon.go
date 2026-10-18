// Package icon draws Kopi's tray icon — two overlapping pages, like a copy
// glyph — in code, so the repository carries no binary image files. It uses
// only the standard library and is deterministic: every call returns
// byte-identical PNGs.
package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
)

// The glyph is defined on a 32-unit design grid; a design coordinate d maps to
// pixel d*size/design. A pixel counts as inside a shape when its centre
// (x+0.5, y+0.5), converted back to design units, is inside.
const (
	design = 32.0

	// Back page: outline only.
	backX0, backY0 = 6.0, 4.0
	backX1, backY1 = 22.0, 24.0

	// Front page: filled, drawn over the back page.
	frontX0, frontY0 = 11.0, 9.0
	frontX1, frontY1 = 27.0, 29.0

	radius = 3.0 // corner radius of both pages
	stroke = 2.5 // back-page outline width

	innerRadius = radius - stroke // corner radius of the outline's hole
)

// Kopi blue, the tray-icon colour on Windows/Linux.
var kopiBlue = color.NRGBA{R: 0x2D, G: 0x7F, B: 0xF9, A: 255}

// templateBlack is the macOS template colour; the system recolors it.
var templateBlack = color.NRGBA{R: 0, G: 0, B: 0, A: 255}

// Template returns a PNG for the macOS menu bar: black glyph (0,0,0,255) on
// transparent, 44x44 pixels (22pt @2x). macOS recolors template images for
// light/dark mode.
func Template() []byte {
	return encodePNG(rasterize(44, templateBlack))
}

// Color returns a PNG of the glyph in Kopi blue (R=0x2D, G=0x7F, B=0xF9,
// A=255), size x size pixels, on transparent.
func Color(size int) []byte {
	return encodePNG(rasterize(size, kopiBlue))
}

// ICO returns a Windows .ico file containing two PNG-compressed images,
// 16x16 and 32x32, both from Color().
func ICO() []byte {
	const (
		dirSize   = 6  // ICONDIR
		entrySize = 16 // ICONDIRENTRY
	)
	sub := [][]byte{Color(16), Color(32)}

	var buf bytes.Buffer
	var scratch [8]byte
	w := func(n int, v uint64) {
		binary.LittleEndian.PutUint64(scratch[:], v)
		buf.Write(scratch[:n])
	}
	w(2, 0) // reserved
	w(2, 1) // type: icon
	w(2, uint64(len(sub)))

	offset := dirSize + entrySize*len(sub)
	for i, p := range sub {
		dim := uint64(16 << i) // 16, then 32
		w(1, dim)              // width
		w(1, dim)              // height
		w(1, 0)                // colour count
		w(1, 0)                // reserved
		w(2, 1)                // planes
		w(2, 32)               // bit count
		w(4, uint64(len(p)))
		w(4, uint64(offset))
		offset += len(p)
	}
	for _, p := range sub {
		buf.Write(p)
	}
	return buf.Bytes()
}

// rasterize paints the glyph at the given pixel size in col.
func rasterize(size int, col color.NRGBA) image.Image {
	if size < 1 {
		size = 1
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := design / float64(size)
	for y := range size {
		for x := range size {
			dx := (float64(x) + 0.5) * scale
			dy := (float64(y) + 0.5) * scale
			if onGlyph(dx, dy) {
				img.SetNRGBA(x, y, col)
			}
		}
	}
	return img
}

// onGlyph reports whether design point (x, y) is on the glyph: inside the
// filled front page, or on the back page's outline (outer rounded rect minus
// the concentric inner one).
func onGlyph(x, y float64) bool {
	if insideRoundRect(x, y, frontX0, frontY0, frontX1, frontY1, radius) {
		return true
	}
	return insideRoundRect(x, y, backX0, backY0, backX1, backY1, radius) &&
		!insideRoundRect(x, y, backX0+stroke, backY0+stroke, backX1-stroke, backY1-stroke, innerRadius)
}

// insideRoundRect reports whether (x, y) lies in the rounded rectangle
// [x0,y0]-[x1,y1] with corner radius r.
func insideRoundRect(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	// In a corner square, the test is against the corner circle.
	if x < x0+r {
		if y < y0+r {
			return sq(x-(x0+r))+sq(y-(y0+r)) <= r*r
		}
		if y > y1-r {
			return sq(x-(x0+r))+sq(y-(y1-r)) <= r*r
		}
		return true
	}
	if x > x1-r {
		if y < y0+r {
			return sq(x-(x1-r))+sq(y-(y0+r)) <= r*r
		}
		if y > y1-r {
			return sq(x-(x1-r))+sq(y-(y1-r)) <= r*r
		}
		return true
	}
	return true
}

func sq(v float64) float64 { return v * v }

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic("icon: png encode failed: " + err.Error())
	}
	return buf.Bytes()
}
