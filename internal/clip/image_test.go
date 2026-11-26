package clip

import (
	"encoding/binary"
	"image/color"
	"testing"
)

func TestIMG1_PaddedBottomUpDIB(t *testing.T) {
	b := make([]byte, 48)
	le := binary.LittleEndian
	le.PutUint32(b, 40)
	le.PutUint32(b[4:], 1)
	le.PutUint32(b[8:], 2)
	le.PutUint16(b[12:], 1)
	le.PutUint16(b[14:], 24)
	copy(b[40:], []byte{255, 0, 0, 0, 0, 0, 255, 0})
	data, err := dibPNG(b)
	if err != nil {
		t.Fatal(err)
	}
	img, err := decodePNG(data)
	if err != nil {
		t.Fatal(err)
	}
	if color.NRGBAModel.Convert(img.At(0, 0)) != (color.NRGBA{R: 255, A: 255}) || color.NRGBAModel.Convert(img.At(0, 1)) != (color.NRGBA{B: 255, A: 255}) {
		t.Fatal("incorrect bitmap colors or orientation")
	}
	if _, err := dibPNG(b[:47]); err == nil {
		t.Fatal("accepted truncated bitmap")
	}
	le.PutUint32(b[4:], 1<<30)
	if _, err := dibPNG(b); err == nil {
		t.Fatal("accepted oversized dimensions")
	}
}
