package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	qrcode "github.com/skip2/go-qrcode"
)

func qrJPEG(t *testing.T, payload string) []byte {
	t.Helper()
	code, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if err = jpeg.Encode(&data, code.Image(512), &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestQRPayloadImagesAndPairingLink(t *testing.T) {
	const payload = "https://weixin.qq.com/pair/actual-qr-payload"
	jpg := qrJPEG(t, payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, name := range []string{"Authorization", "AuthorizationType", "X-WECHAT-UIN", "iLink-App-Id", "Cookie"} {
			if r.Header.Get(name) != "" {
				t.Errorf("QR image request includes %s", name)
			}
		}
		switch r.URL.Path {
		case "/image.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(jpg)
		case "/opaque":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(jpg)
		case "/redirect":
			http.Redirect(w, r, "/image.jpg", http.StatusFound)
		case "/pair":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<!doctype html><title>WeChat</title>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, content := range []string{server.URL + "/image.jpg", server.URL + "/opaque", server.URL + "/redirect", "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpg), base64.StdEncoding.EncodeToString(jpg)} {
		got, err := loginQRPayload(context.Background(), server.Client(), content)
		if err != nil || got != payload {
			t.Fatalf("image QR payload: got %q, error %v", got, err)
		}
	}
	link := server.URL + "/pair"
	if got, err := loginQRPayload(context.Background(), server.Client(), link); err != nil || got != link {
		t.Fatalf("pairing link changed: %q %v", got, err)
	}
}

// Turn the actual terminal glyphs back into pixels, then independently scan
// them. This catches inverted colors, incorrect half blocks, and encoding the
// image URL/base64 instead of the QR payload inside the server image.
func terminalQRImage(t *testing.T, output string) []byte {
	t.Helper()
	var rows [][]rune
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "\x1b[30;47m") {
			rows = append(rows, []rune(strings.TrimSuffix(strings.TrimPrefix(line, "\x1b[30;47m"), "\x1b[0m")))
		}
	}
	if len(rows) == 0 {
		t.Fatal("no terminal QR rendered")
	}
	const scale = 8
	img := image.NewGray(image.Rect(0, 0, len(rows[0])*scale, len(rows)*2*scale))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	for y, row := range rows {
		for x, glyph := range row {
			for half := range 2 {
				black := glyph == '█' || half == 0 && glyph == '▀' || half == 1 && glyph == '▄'
				if black {
					for yy := range scale {
						for xx := range scale {
							img.SetGray(x*scale+xx, (y*2+half)*scale+yy, color.Gray{Y: 0})
						}
					}
				}
			}
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestLoginRendersScannableTerminalAndPNGFromJPEG(t *testing.T) {
	const payload = "weixin://pair/real-payload"
	content := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(qrJPEG(t, payload))
	var out bytes.Buffer
	file := filepath.Join(t.TempDir(), "login.png")
	if err := displayLoginQR(context.Background(), http.DefaultClient, &out, QRCode{Content: content}, file); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "base64,") {
		t.Fatal("image contents printed instead of decoded payload")
	}
	terminal := terminalQRImage(t, out.String())
	if got, err := decodeQRImage(terminal); err != nil || got != payload {
		t.Fatalf("terminal scan: %q %v", got, err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := decodeQRImage(data); err != nil || got != payload {
		t.Fatalf("PNG scan: %q %v", got, err)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("PNG permissions: %v %v", info, err)
	}
}

func TestQRRejectsMalformedAndOversizedImages(t *testing.T) {
	for _, content := range []string{"data:image/jpeg;base64,broken!", "data:image/jpeg,not-base64", "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not an image"))} {
		if _, err := loginQRPayload(context.Background(), http.DefaultClient, content); err == nil {
			t.Fatal("accepted invalid QR image")
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewGray(image.Rect(0, 0, 5000, 1))); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeQRImage(data.Bytes()); err == nil {
		t.Fatal("accepted excessive image dimensions")
	}
	if _, err := decodeQRImage(make([]byte, maxQRImageBytes+1)); err == nil {
		t.Fatal("accepted oversized image")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("broken image"))
	}))
	defer server.Close()
	if _, err := loginQRPayload(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("encoded malformed image URL as a pairing payload")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := loginQRPayload(ctx, server.Client(), server.URL); err != context.Canceled {
		t.Fatalf("cancellation lost: %v", err)
	}
}
