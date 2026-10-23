package native

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const MaxAttachmentBytes = 32 * 1024 * 1024

type Attachment struct {
	Kind, Name     string
	mediaID, appID string
	size           uint64
}

func AttachmentFor(m Message) (Attachment, bool) {
	if m.Type == 3 {
		return Attachment{Kind: "image", Name: m.ID + ".jpg"}, true
	}
	if m.Type == 34 {
		return Attachment{Kind: "voice", Name: "Voice message"}, true
	}
	if m.Type == 43 || m.Type == 62 {
		return Attachment{Kind: "video", Name: "Video"}, true
	}
	if m.Type != 49 {
		return Attachment{}, false
	}
	var data struct {
		App struct {
			ID     string `xml:"appid,attr"`
			Type   int    `xml:"type"`
			Title  string `xml:"title"`
			Attach struct {
				ID     string `xml:"attachid"`
				CDN    string `xml:"cdnattachurl"`
				Length uint64 `xml:"totallen"`
			} `xml:"appattach"`
		} `xml:"appmsg"`
	}
	text := m.Text
	if i := strings.IndexByte(text, '<'); i >= 0 {
		text = text[i:]
	}
	if len(text) > 128*1024 || xml.Unmarshal([]byte(text), &data) != nil || data.App.Type != 6 {
		return Attachment{}, false
	}
	id := data.App.Attach.ID
	if id == "" {
		id = data.App.Attach.CDN
	}
	return Attachment{Kind: "file", Name: data.App.Title, mediaID: id, appID: data.App.ID, size: data.App.Attach.Length}, true
}
func validateAttachmentChunk(response []byte, offset, total uint64) ([]byte, uint64, error) {
	if err := requireSuccess(response); err != nil {
		return nil, 0, err
	}
	size, err := getInt(response, 5)
	if err != nil {
		return nil, 0, err
	}
	start, err := getInt(response, 6)
	if err != nil {
		return nil, 0, err
	}
	declared, err := getInt(response, 7)
	if err != nil {
		return nil, 0, err
	}
	data, err := optionalBuffer(response, 8)
	if err != nil {
		return nil, 0, err
	}
	if size == 0 || size > MaxAttachmentBytes || start != offset || declared != uint64(len(data)) || len(data) == 0 || offset+declared > size || (total != 0 && size != total) {
		return nil, 0, errors.New("invalid or oversized attachment chunk")
	}
	return data, size, nil
}

// DownloadAttachment downloads bounded chunks through the authenticated native
// session. It never follows a URL supplied in a message or executes a file.
func (s *Session) DownloadAttachment(ctx context.Context, a *Account, m Message, out io.Writer) error {
	if err := a.Validate(); err != nil {
		return err
	}
	attachment, ok := AttachmentFor(m)
	if !ok {
		return errors.New("message has no downloadable attachment")
	}
	if attachment.Kind != "image" && attachment.Kind != "file" {
		return fmt.Errorf("%s downloads are not implemented yet", attachment.Kind)
	}
	id, err := strconv.ParseUint(m.ID, 10, 64)
	if err != nil || id == 0 {
		return errors.New("attachment has no server message ID")
	}
	if attachment.Kind == "file" && (attachment.mediaID == "" || len(attachment.mediaID) > 4096) {
		return errors.New("file has no supported native attachment identifier")
	}
	if attachment.size > MaxAttachmentBytes {
		return errors.New("attachment exceeds 32 MiB limit")
	}
	p, _ := profileFor(a.Profile)
	var offset, total uint64
	if attachment.Kind == "file" {
		total = attachment.size
	}
	for offset == 0 || offset < total {
		var request []byte
		command, cgi := uint32(10), uint64(109)
		if attachment.Kind == "image" {
			request = message(pb(1, p.baseRequest(a.Device, uint64(a.UIN), 0)), pv(2, 0), pb(3, pb(1, []byte(m.From))), pb(4, pb(1, []byte(m.To))), pv(5, total), pv(6, offset), pv(7, 64*1024), pv(8, 0), pv(9, id))
		} else {
			command, cgi = 106, 106
			request = message(pb(1, p.baseRequest(a.Device, uint64(a.UIN), 0)), pb(2, []byte(attachment.appID)), pv(3, 0), pb(4, []byte(attachment.mediaID)), pb(5, []byte(a.Username)), pv(6, total), pv(7, offset), pv(8, 64*1024), pb(9, nil), pv(10, 0), pv(11, 0), pv(12, 0), pv(13, id))
		}
		response, err := s.authenticated(ctx, a, command, cgi, request)
		if err != nil {
			return err
		}
		chunk, size, err := validateAttachmentChunk(response, offset, total)
		if err != nil {
			return err
		}
		if attachment.Kind == "image" {
			returned, err := optionalInt(response, 9)
			if err != nil {
				return err
			}
			if returned != 0 && returned != id {
				return errors.New("attachment response message ID mismatch")
			}
		}
		n, err := out.Write(chunk)
		if err != nil {
			return err
		}
		if n != len(chunk) {
			return io.ErrShortWrite
		}
		total = size
		offset += uint64(n)
	}
	return nil
}
