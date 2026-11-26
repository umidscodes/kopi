package sync

import (
	"github.com/umidscodes/kopi/internal/clip"
	"github.com/umidscodes/kopi/internal/proto"
	"testing"
)

func TestIMG1_EncryptedImageAndEcho(t *testing.T) {
	net := newNet()
	a, b := net.add(t, "a"), net.add(t, "b")
	// Binary payload includes bytes text normalization would change.
	item := clip.Item{PNG: "\x89PNG\r\n\x1a\n\x00\xff"}
	a.engine.HandleLocal(clip.Item{PNG: item.PNG, Sensitive: true})
	if a.sentCount() != 0 {
		t.Fatal("private image sent")
	}
	a.engine.HandleLocal(item)
	if a.sentCount() != 1 || b.clip.Writes() != 1 {
		t.Fatalf("image delivery failed: %v", b.errors())
	}
	m, err := proto.Open(keyAlpha, a.sent[0])
	if err != nil || m.PNG != item.PNG || m.Text != "" {
		t.Fatal("image changed on wire", err)
	}
	b.engine.HandleLocal(item)
	if b.sentCount() != 0 {
		t.Fatal("image echoed")
	}
	b.engine.HandleLocal(clip.Item{Text: "after screenshot"})
	if a.clip.Text() != "after screenshot" {
		t.Fatal("text following image was lost")
	}
}
