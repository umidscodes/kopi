package clip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"strings"

	"github.com/umidscodes/kopi/internal/proto"
)

const maxPixels = 16 * 1024 * 1024

var errImage = errors.New("unsupported or oversized clipboard image")

func validDimensions(w, h int) bool { return w > 0 && h > 0 && w <= maxPixels/h }

func decodePNG(data string) (image.Image, error) {
	if len(data) > proto.MaxTextBytes {
		return nil, errImage
	}
	cfg, err := png.DecodeConfig(strings.NewReader(data))
	if err != nil || !validDimensions(cfg.Width, cfg.Height) {
		return nil, errImage
	}
	return png.Decode(strings.NewReader(data))
}

// dibPNG reads uncompressed 24/32-bit Windows DIBs. BI_RGB reserves the fourth byte.
func dibPNG(b []byte) (string, error) {
	if len(b) < 40 {
		return "", errImage
	}
	le := binary.LittleEndian
	header := int(le.Uint32(b))
	w, h := int(int32(le.Uint32(b[4:]))), int(int32(le.Uint32(b[8:])))
	top := h < 0
	if top {
		h = -h
	}
	bits := int(le.Uint16(b[14:]))
	if header < 40 || header > len(b) || !validDimensions(w, h) || le.Uint16(b[12:]) != 1 || (bits != 24 && bits != 32) || le.Uint32(b[16:]) != 0 {
		return "", errImage
	}
	offset := int64(header) + int64(le.Uint32(b[32:]))*4
	stride := (w*(bits/8) + 3) &^ 3
	if offset+int64(stride)*int64(h) > int64(len(b)) {
		return "", errImage
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := h - 1 - y
		if top {
			row = y
		}
		src := b[int(offset)+row*stride:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			i := x * (bits / 8)
			j := 4 * x
			dst[j], dst[j+1], dst[j+2], dst[j+3] = src[i+2], src[i+1], src[i], 255
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return "", err
	}
	return out.String(), nil
}
