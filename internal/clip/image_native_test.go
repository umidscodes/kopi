//go:build windows || darwin

package clip

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestIMG1_NativeClipboard(t *testing.T) {
	c := realClipboard(t)
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(1, 0, color.NRGBA{R: 234, A: 123})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := c.WritePNG(b.String()); err != nil {
		t.Fatal(err)
	}
	got, err := read()
	if err != nil || got.PNG != b.String() || got.Sensitive {
		t.Fatalf("native PNG round trip failed: %v", err)
	}
}
