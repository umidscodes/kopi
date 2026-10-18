package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"testing"
)

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	return img
}

// rgba returns the premultiplied RGBA components of a pixel.
func rgba(t *testing.T, img image.Image, x, y int) (r, g, b, a uint32) {
	t.Helper()
	return img.At(x, y).RGBA()
}

func wantRGBA(t *testing.T, img image.Image, x, y int, wr, wg, wb, wa uint32) {
	t.Helper()
	r, g, b, a := rgba(t, img, x, y)
	if r != wr || g != wg || b != wb || a != wa {
		t.Fatalf("pixel (%d,%d) = RGBA(%#04x,%#04x,%#04x,%#04x), want RGBA(%#04x,%#04x,%#04x,%#04x)",
			x, y, r, g, b, a, wr, wg, wb, wa)
	}
}

func TestTemplateIs44x44BlackOnTransparent(t *testing.T) {
	img := decode(t, Template())
	if b := img.Bounds(); b.Dx() != 44 || b.Dy() != 44 {
		t.Fatalf("size = %dx%d, want 44x44", b.Dx(), b.Dy())
	}
	if _, _, _, a := rgba(t, img, 0, 0); a != 0 {
		t.Fatalf("pixel (0,0) alpha = %#x, want 0", a)
	}
	opaqueBlack := 0
	for y := range 44 {
		for x := range 44 {
			r, g, b, a := rgba(t, img, x, y)
			switch {
			case a == 0:
				if r != 0 || g != 0 || b != 0 {
					t.Fatalf("pixel (%d,%d): transparent but RGB=(%#04x,%#04x,%#04x)", x, y, r, g, b)
				}
			case a == 0xffff && r == 0 && g == 0 && b == 0:
				opaqueBlack++
			default:
				t.Fatalf("pixel (%d,%d) = RGBA(%#04x,%#04x,%#04x,%#04x): must be transparent or black", x, y, r, g, b, a)
			}
		}
	}
	if opaqueBlack == 0 {
		t.Fatal("no opaque black pixels in template")
	}
}

func TestColor32IsKopiBlueGlyph(t *testing.T) {
	img := decode(t, Color(32))
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
		t.Fatalf("size = %dx%d, want 32x32", b.Dx(), b.Dy())
	}
	if _, _, _, a := rgba(t, img, 0, 0); a != 0 {
		t.Fatalf("pixel (0,0) alpha = %#x, want 0", a)
	}
	const blueR, blueG, blueB = 0x2d<<8 | 0x2d, 0x7f<<8 | 0x7f, 0xf9<<8 | 0xf9
	found := false
	for y := range 32 {
		for x := range 32 {
			r, g, b, a := rgba(t, img, x, y)
			if a == 0 {
				continue
			}
			if a != 0xffff || r != blueR || g != blueG || b != blueB {
				t.Fatalf("pixel (%d,%d) = RGBA(%#04x,%#04x,%#04x,%#04x), want Kopi blue or transparent", x, y, r, g, b, a)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no Kopi blue pixels")
	}
}

func TestFrontPageFilledBackPageInteriorHollow(t *testing.T) {
	img := decode(t, Color(32))
	// Design (19,19) sits inside the filled front page (11,9)-(27,29): its
	// pixel centre (19.5,19.5) is Kopi blue.
	wantRGBA(t, img, 19, 19, 0x2d<<8|0x2d, 0x7f<<8|0x7f, 0xf9<<8|0xf9, 0xffff)
	// Design point (9.5,7.5) (pixel (9,7)) is inside the back rectangle
	// (6,4)-(22,24) but more than stroke=2.5 from its edges (3.5 from left
	// and top), outside the front rectangle (11,9)-(27,29), and not on the
	// outline: it must be transparent.
	if _, _, _, a := rgba(t, img, 9, 7); a != 0 {
		t.Fatalf("back page interior pixel (9,7) alpha = %#x, want 0 (hollow)", a)
	}
	// Sanity: pixel (6,5), centre (6.5,5.5), is inside the outer rectangle
	// and 2.9 units from the corner circle centre (9,7) (< r=3), i.e. on the
	// stroke, so the outline really is drawn.
	wantRGBA(t, img, 6, 5, 0x2d<<8|0x2d, 0x7f<<8|0x7f, 0xf9<<8|0xf9, 0xffff)
}

func TestICOHeaderAndEmbeddedPNGs(t *testing.T) {
	data := ICO()
	if len(data) < 6+2*16 {
		t.Fatalf("ICO too short: %d bytes", len(data))
	}
	if got := binary.LittleEndian.Uint16(data[0:2]); got != 0 {
		t.Fatalf("reserved = %d, want 0", got)
	}
	if got := binary.LittleEndian.Uint16(data[2:4]); got != 1 {
		t.Fatalf("type = %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint16(data[4:6]); got != 2 {
		t.Fatalf("count = %d, want 2", got)
	}
	const dirSize = 6
	for i, wantDim := range []int{16, 32} {
		e := data[dirSize+i*16 : dirSize+(i+1)*16]
		if int(e[0]) != wantDim || int(e[1]) != wantDim {
			t.Fatalf("entry %d: dims = %dx%d, want %dx%d", i, e[0], e[1], wantDim, wantDim)
		}
		if e[2] != 0 || e[3] != 0 {
			t.Fatalf("entry %d: colorCount/reserved = %d/%d, want 0/0", i, e[2], e[3])
		}
		if got := binary.LittleEndian.Uint16(e[4:6]); got != 1 {
			t.Fatalf("entry %d: planes = %d, want 1", i, got)
		}
		if got := binary.LittleEndian.Uint16(e[6:8]); got != 32 {
			t.Fatalf("entry %d: bitCount = %d, want 32", i, got)
		}
		size := binary.LittleEndian.Uint32(e[8:12])
		off := binary.LittleEndian.Uint32(e[12:16])
		if int(off)+int(size) > len(data) {
			t.Fatalf("entry %d: offset %d + size %d exceeds file len %d", i, off, size, len(data))
		}
		img, err := png.Decode(bytes.NewReader(data[off : off+size]))
		if err != nil {
			t.Fatalf("entry %d: bytes at offset %d: %v", i, off, err)
		}
		if b := img.Bounds(); b.Dx() != wantDim || b.Dy() != wantDim {
			t.Fatalf("entry %d: decoded %dx%d, want %dx%d", i, b.Dx(), b.Dy(), wantDim, wantDim)
		}
	}
}

func TestDeterministic(t *testing.T) {
	if !bytes.Equal(Template(), Template()) {
		t.Error("Template not deterministic")
	}
	if !bytes.Equal(Color(32), Color(32)) {
		t.Error("Color not deterministic")
	}
	if !bytes.Equal(ICO(), ICO()) {
		t.Error("ICO not deterministic")
	}
}
