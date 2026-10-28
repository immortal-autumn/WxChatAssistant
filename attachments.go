package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/immortal-autumn/WxChatAssistant/internal/native"
)

type chatAttachmentResult struct {
	Message       native.Message
	Path, Preview string
	Err           error
}

func safeAttachmentName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || strings.ContainsRune(" ._-", r) {
			return r
		}
		return '_'
	}, name)
	runes := []rune(name)
	if len(runes) > 100 {
		runes = runes[:100]
	}
	name = strings.Trim(string(runes), " .")
	if name == "" {
		name = "attachment"
	}
	return name
}
func (c *chatService) attachment(ctx context.Context, m native.Message) chatAttachmentResult {
	result := chatAttachmentResult{Message: m}
	meta, ok := native.AttachmentFor(m)
	if !ok {
		result.Err = errors.New("message has no attachment")
		return result
	}
	if meta.Kind != "image" && meta.Kind != "file" {
		result.Err = fmt.Errorf("%s downloads are not supported yet", meta.Kind)
		return result
	}
	dir := filepath.Join(c.store.dir, "downloads")
	if err := os.MkdirAll(dir, 0700); err != nil {
		result.Err = err
		return result
	}
	hash := sha256.Sum256([]byte(c.owner + "/" + m.ID))
	prefix := fmt.Sprintf("%x-", hash[:8])
	name := safeAttachmentName(meta.Name)
	path := filepath.Join(dir, prefix+name)
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		result.Path = path
		return result
	}
	lock, err := c.store.lock(ctx)
	if err != nil {
		result.Err = err
		return result
	}
	defer lock.Close()
	state, err := c.store.state()
	if err != nil {
		result.Err = err
		return result
	}
	if state.Account == nil || state.Account.Username != c.owner {
		result.Err = errors.New("account changed")
		return result
	}
	session, err := native.Connect(ctx, state.Account.Host)
	if err != nil {
		result.Err = err
		return result
	}
	defer session.Close()
	file, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		result.Err = err
		return result
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err = session.DownloadAttachment(ctx, state.Account, m, file); err != nil {
		result.Err = err
		return result
	}
	if err = file.Sync(); err != nil {
		result.Err = err
		return result
	}
	if err = file.Close(); err != nil {
		result.Err = err
		return result
	}
	if err = os.Rename(file.Name(), path); err != nil {
		result.Err = err
		return result
	}
	if err = c.store.save("state.json", state); err != nil {
		result.Err = err
		return result
	}
	result.Path = path
	return result
}
func imagePreview(path string, columns, rows int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(io.LimitReader(file, native.MaxAttachmentBytes))
	if err != nil {
		return "", errors.New("this image format cannot be rendered; the downloaded file is available at the saved path")
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 32*1024*1024 {
		return "", errors.New("image dimensions exceed the preview limit")
	}
	if _, err = file.Seek(0, 0); err != nil {
		return "", err
	}
	picture, _, err := image.Decode(io.LimitReader(file, native.MaxAttachmentBytes))
	if err != nil {
		return "", err
	}
	width := min(max(1, columns), config.Width)
	height := max(1, config.Height*width/config.Width)
	if height > max(1, rows)*2 {
		height = max(1, rows) * 2
		width = max(1, config.Width*height/config.Height)
	}
	bounds := picture.Bounds()
	var out strings.Builder
	for y := 0; y < height; y += 2 {
		for x := 0; x < width; x++ {
			r, g, b, _ := picture.At(bounds.Min.X+x*config.Width/width, bounds.Min.Y+y*config.Height/height).RGBA()
			r2, g2, b2, _ := picture.At(bounds.Min.X+x*config.Width/width, bounds.Min.Y+min(y+1, height-1)*config.Height/height).RGBA()
			fmt.Fprintf(&out, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", r>>8, g>>8, b>>8, r2>>8, g2>>8, b2>>8)
		}
		out.WriteString("\x1b[0m\n")
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}
func attachmentCommand(ctx context.Context, store Store, args []string) error {
	f := flags("attachment")
	id := f.String("id", "", "server message ID from read/history")
	output := f.String("output", "", "save a copy to this path")
	preview := f.Bool("preview", false, "render supported images as terminal blocks")
	if err := parse(f, args); err != nil {
		return err
	}
	if *id == "" {
		return errors.New("--id is required")
	}
	s, err := openNativeStore(store)
	if err != nil {
		return err
	}
	state, err := s.state()
	if err != nil {
		return err
	}
	if state.Account == nil {
		return errors.New("not logged in")
	}
	history, err := s.history(state.Account.Username)
	if err != nil {
		return err
	}
	var message *native.Message
	for i := range history {
		if history[i].ID == *id {
			message = &history[i]
			break
		}
	}
	if message == nil {
		return errors.New("message is not in the local archive")
	}
	service := &chatService{store: s, owner: state.Account.Username}
	result := service.attachment(ctx, *message)
	if result.Err != nil {
		return result.Err
	}
	if *output != "" {
		data, err := os.ReadFile(result.Path)
		if err != nil {
			return err
		}
		if err = os.WriteFile(*output, data, 0600); err != nil {
			return err
		}
		result.Path = *output
	}
	fmt.Fprintln(os.Stderr, "Saved:", result.Path)
	if *preview {
		view, err := imagePreview(result.Path, 80, 30)
		if err != nil {
			return err
		}
		fmt.Println(view)
	} else {
		fmt.Println(result.Path)
	}
	return nil
}
