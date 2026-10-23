package main

import (
	"github.com/charmbracelet/lipgloss"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImagePreviewAndSafeFilename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 40, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 40; x++ {
			picture.Set(x, y, color.RGBA{R: uint8(x * 6), G: uint8(y * 3), B: 100, A: 255})
		}
	}
	if err = png.Encode(f, picture); err != nil {
		t.Fatal(err)
	}
	f.Close()
	preview, err := imagePreview(path, 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	if lipgloss.Width(preview) > 20 || lipgloss.Height(preview) > 10 || !strings.Contains(preview, "38;2;") {
		t.Fatal("preview did not fit terminal bounds")
	}
	for _, name := range []string{"../../secret.txt", "..\\..\\secret.txt", "\x1b[2Jbad.png"} {
		safe := safeAttachmentName(name)
		if strings.ContainsAny(safe, "/\\\x1b") || safe == ".." {
			t.Fatal("unsafe attachment path", safe)
		}
	}
}
