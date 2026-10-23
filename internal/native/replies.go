package native

import (
	"context"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Reference struct {
	Type    int32  `xml:"type"`
	ID      string `xml:"svrid"`
	From    string `xml:"fromusr"`
	Sender  string `xml:"chatusr"`
	Name    string `xml:"displayname"`
	Content string `xml:"content"`
	Source  string `xml:"msgsource"`
}
type replyApp struct {
	XMLName     xml.Name  `xml:"appmsg"`
	AppID       string    `xml:"appid,attr"`
	SDK         string    `xml:"sdkver,attr"`
	Title       string    `xml:"title"`
	Description string    `xml:"des"`
	Action      string    `xml:"action"`
	Type        int       `xml:"type"`
	ShowType    int       `xml:"showtype"`
	SoundType   int       `xml:"soundtype"`
	URL         string    `xml:"url"`
	Reference   Reference `xml:"refermsg"`
}

func ReferenceFor(m Message) (Reference, bool) {
	if m.Type != 49 || len(m.Text) > 128*1024 {
		return Reference{}, false
	}
	text := m.Text
	if i := strings.IndexByte(text, '<'); i >= 0 {
		text = text[i:]
	}
	var outer struct {
		App replyApp `xml:"appmsg"`
	}
	var app replyApp
	if strings.HasPrefix(strings.TrimSpace(text), "<appmsg") {
		if xml.Unmarshal([]byte(text), &app) != nil {
			return Reference{}, false
		}
	} else {
		if xml.Unmarshal([]byte(text), &outer) != nil {
			return Reference{}, false
		}
		app = outer.App
	}
	return app.Reference, app.Type == 57 && app.Reference.ID != ""
}
func replyContent(target Message, text, name string) (string, error) {
	NormalizeMessage(&target)
	id, err := strconv.ParseUint(target.ID, 10, 64)
	if err != nil || id == 0 || target.Deleted || target.Peer == "" || target.Type <= 0 || strings.TrimSpace(text) == "" || len(text)+len(target.Text) > 48*1024 {
		return "", errors.New("invalid, deleted, or oversized reply target")
	}
	sender := target.Sender
	if sender == "" {
		sender = target.From
	}
	if strings.HasSuffix(target.From, "@chatroom") && target.Sender == "" {
		return "", errors.New("cannot identify the participant for this group reply")
	}
	app := replyApp{SDK: "0", Title: text, Type: 57, Reference: Reference{Type: target.Type, ID: target.ID, From: target.From, Sender: sender, Name: name, Content: target.Text}}
	data, err := xml.Marshal(app)
	return string(data), err
}
func (s *Session) SendReply(ctx context.Context, a *Account, target Message, text, name string) (Message, error) {
	var result Message
	if err := a.Validate(); err != nil {
		return result, err
	}
	if len(target.Peer) > 256 || strings.ContainsAny(target.Peer, "\x00\r\n") {
		return result, errors.New("invalid reply recipient")
	}
	content, err := replyContent(target, text, name)
	if err != nil {
		return result, err
	}
	random, err := randomBytes(16)
	if err != nil {
		return result, err
	}
	clientID := "wechat-cli-" + hex.EncodeToString(random)
	now := uint64(time.Now().Unix())
	p, _ := profileFor(a.Profile)
	app := message(pb(1, []byte(a.Username)), pb(2, nil), pv(3, 0), pb(4, []byte(target.Peer)), pv(5, 57), pb(6, []byte(content)), pv(7, now), pb(8, []byte(clientID)), pv(10, 1))
	request := message(pb(1, p.baseRequest(a.Device, uint64(a.UIN), 0)), pb(2, app), pv(4, now), pv(9, 0), pv(10, 0), pv(11, 0))
	response, err := s.authenticated(ctx, a, 0, 222, request)
	if err != nil {
		return result, fmt.Errorf("reply outcome uncertain; do not automatically retry: %w", err)
	}
	if err = requireSuccess(response); err != nil {
		return result, err
	}
	for _, echo := range []struct {
		field    uint64
		expected string
	}{{3, a.Username}, {4, target.Peer}, {6, clientID}} {
		value, err := optionalBytes(response, echo.field)
		if err != nil {
			return result, err
		}
		if len(value) > 0 && string(value) != echo.expected {
			return result, errors.New("reply outcome uncertain: receipt identity mismatch")
		}
	}
	id, err := getInt(response, 9)
	if err != nil || id == 0 {
		return result, errors.New("reply outcome uncertain: missing server message ID")
	}
	timestamp, _ := optionalInt(response, 7)
	if timestamp == 0 || timestamp > 0xffffffff {
		timestamp = now
	}
	result = Message{ID: strconv.FormatUint(id, 10), From: a.Username, To: target.Peer, Peer: target.Peer, Type: 49, Text: "<msg>" + content + "</msg>", Timestamp: uint32(timestamp), Outgoing: true}
	return result, nil
}
