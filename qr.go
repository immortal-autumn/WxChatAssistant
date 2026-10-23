package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/makiuchi-d/gozxing"
	qrreader "github.com/makiuchi-d/gozxing/qrcode"
	qrcode "github.com/skip2/go-qrcode"
)

const maxQRImageBytes = 4 << 20

// The API can return either a pairing URL/payload or an image containing it.
func loginQRPayload(ctx context.Context, client *http.Client, content string) (string, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "data:image/") {
		metadata, encoded, ok := strings.Cut(content, ",")
		if !ok || !strings.HasSuffix(metadata, ";base64") {
			return "", errors.New("QR image must use base64 encoding")
		}
		return decodeBase64QR(encoded)
	}
	if strings.HasPrefix(content, "http://") || strings.HasPrefix(content, "https://") {
		return fetchQRPayload(ctx, client, content)
	}
	// Raw base64 images have no data-URI prefix. Avoid treating ordinary payloads
	// as images unless the decoded bytes have a supported image signature.
	if len(content) <= base64.StdEncoding.EncodedLen(maxQRImageBytes) {
		if data, err := base64.StdEncoding.DecodeString(content); err == nil && isQRImage(data) {
			return decodeQRImage(data)
		}
	}
	if content == "" {
		return "", errors.New("empty QR content")
	}
	return content, nil
}

func isQRImage(data []byte) bool {
	return bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}) || bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n"))
}

func decodeBase64QR(encoded string) (string, error) {
	if len(encoded) > base64.StdEncoding.EncodedLen(maxQRImageBytes) {
		return "", errors.New("QR image exceeds 4 MiB")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("invalid base64 QR image")
	}
	return decodeQRImage(data)
}

func decodeQRImage(data []byte) (string, error) {
	if len(data) > maxQRImageBytes {
		return "", errors.New("QR image exceeds 4 MiB")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("QR content is not a supported JPEG or PNG image")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 4096 || config.Height > 4096 || int64(config.Width)*int64(config.Height) > 8<<20 {
		return "", errors.New("QR image dimensions are too large")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("cannot read QR image")
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", errors.New("cannot prepare QR image for scanning")
	}
	result, err := qrreader.NewQRCodeReader().Decode(bitmap, map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_TRY_HARDER: true})
	if err != nil || result.GetText() == "" {
		return "", errors.New("cannot scan a QR code from the server image")
	}
	return result.GetText(), nil
}

func fetchQRPayload(ctx context.Context, client *http.Client, content string) (string, error) {
	u, err := url.Parse(content)
	if err != nil {
		return "", errors.New("invalid QR content URL")
	}
	extension := strings.ToLower(path.Ext(u.Path))
	imageURL := extension == ".jpg" || extension == ".jpeg" || extension == ".png"
	// A normal pairing link may point to an HTML landing page or be inaccessible
	// outside WeChat. It remains valid QR text when it doesn't return an image.
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, content, nil)
	if err != nil {
		return "", errors.New("invalid QR content URL")
	}
	// Fetch image data without any bot credentials, cookies, or API headers.
	images := &http.Client{Transport: client.Transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
			return errors.New("unexpected QR image redirect")
		}
		return nil
	}}
	resp, err := images.Do(req)
	if err != nil {
		if parent.Err() != nil {
			return "", parent.Err()
		}
		if imageURL {
			return "", errors.New("cannot fetch QR image")
		}
		return content, nil
	}
	defer resp.Body.Close()
	imageResponse := strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "image/")
	if resp.StatusCode != http.StatusOK {
		if imageURL || imageResponse {
			return "", fmt.Errorf("QR image HTTP %d", resp.StatusCode)
		}
		return content, nil
	}
	reader := bufio.NewReader(resp.Body)
	header, _ := reader.Peek(8)
	if !imageURL && !imageResponse && !isQRImage(header) {
		return content, nil
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxQRImageBytes+1))
	if err != nil {
		return "", errors.New("cannot read QR image response")
	}
	return decodeQRImage(data)
}

func displayLoginQR(ctx context.Context, client *http.Client, out io.Writer, q QRCode, pngPath string) error {
	payload, err := loginQRPayload(ctx, client, q.Content)
	if err != nil {
		return err
	}
	code, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintln(out, "Scan this QR code in WeChat:"); err != nil {
		return err
	}
	// Explicit colors and the generator's quiet zone make this scan on both
	// light and dark terminals, including SSH sessions. Reset every line.
	for _, row := range strings.Split(strings.TrimSuffix(code.ToSmallString(true), "\n"), "\n") {
		if _, err = fmt.Fprintf(out, "\x1b[30;47m%s\x1b[0m\n", row); err != nil {
			return err
		}
	}
	if _, err = fmt.Fprintln(out, payload); err != nil {
		return err
	}
	if pngPath != "" {
		data, err := code.PNG(512)
		if err != nil {
			return err
		}
		if err = os.WriteFile(pngPath, data, 0600); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, "QR PNG:", pngPath)
		return err
	}
	return nil
}
