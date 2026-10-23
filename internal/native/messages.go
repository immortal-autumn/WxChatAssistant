package native

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/adler32"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

func businessChecksum(uin uint32, plain []byte) uint32 {
	first := md5.Sum(binary.BigEndian.AppendUint32(nil, uin))
	prefix := md5.Sum(append(binary.BigEndian.AppendUint32(nil, uint32(len(plain))), first[:]...))
	h := adler32.New()
	h.Write(prefix[:])
	h.Write(plain)
	return h.Sum32()
}
func (s *Session) authenticated(ctx context.Context, a *Account, command uint32, cgi uint64, plain []byte) ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if len(plain) > 65503-128 {
		return nil, errors.New("native request too large for one record")
	}
	encrypted, err := sealEnvelope(plain, a.RequestKey, nil)
	if err != nil {
		return nil, err
	}
	p, _ := profileFor(a.Profile)
	header := binary.BigEndian.AppendUint32(nil, uint32(p.version))
	header = binary.BigEndian.AppendUint32(header, a.UIN)
	header = append(header, a.Cookie...)
	for _, v := range []uint64{cgi, uint64(len(plain)), uint64(len(plain)), 0, p.deviceID, uint64(businessChecksum(a.UIN, plain))} {
		header = binary.AppendUvarint(header, v)
	}
	seq, err := randomBytes(2)
	if err != nil {
		return nil, err
	}
	header = append(header, 0xff, 0, 0, 0, 0)
	header = append(header, seq...)
	n := len(header) + 3
	if n > 63 {
		return nil, errors.New("native request header too large")
	}
	flags := uint16(n<<2 | 2 | 13<<12 | len(a.Cookie)<<8)
	packet := message([]byte{0xbf, byte(flags), byte(flags >> 8)}, header, encrypted)
	var reply []byte
	if command == 0 {
		path := ""
		switch cgi {
		case 222:
			path = "/cgi-bin/micromsg-bin/sendappmsg"
		case 551:
			path = "/cgi-bin/micromsg-bin/getchatroommemberdetail"
		case 251:
			path = "/cgi-bin/micromsg-bin/statusnotify"
		case 109:
			path = "/cgi-bin/micromsg-bin/getmsgimg"
		case 106:
			path = "/cgi-bin/micromsg-bin/downloadappattach"
		default:
			return nil, errors.New("unsupported shortlink route")
		}
		reply, err = s.shortExchange(ctx, a.shortHost(), path, packet)
	} else {
		reply, err = s.rpc(ctx, command, packet)
	}
	if err != nil {
		return nil, err
	}
	if len(reply) < 11 {
		return nil, errors.New("short native session response")
	}
	offset := 0
	if reply[0] == 0xbf {
		offset = 1
	}
	flags = binary.LittleEndian.Uint16(reply[offset : offset+2])
	boundary := int(flags >> 2 & 63)
	cookieLen := int(flags >> 8 & 15)
	code := int32(binary.BigEndian.Uint32(reply[offset+2 : offset+6]))
	if code != 0 {
		return nil, fmt.Errorf("native session returned status %d; session may need login again", code)
	}
	if boundary < offset+10+cookieLen || boundary > len(reply) {
		return nil, errors.New("invalid native session response boundary")
	}
	metadata := reply[offset+10+cookieLen : boundary]
	var values [3]uint64
	for i := range values {
		v, n := binary.Uvarint(metadata)
		if n <= 0 {
			return nil, errors.New("invalid native session response metadata")
		}
		values[i] = v
		metadata = metadata[n:]
	}
	if values[0] != cgi || flags>>12 != 13 || values[1] > maxResponse || values[2] > maxResponse {
		return nil, errors.New("unsupported native response policy")
	}
	body, err := openEnvelope(reply[boundary:], a.ResponseKey, nil)
	if err != nil {
		return nil, err
	}
	if uint64(len(body)) != values[2] {
		return nil, errors.New("native response size mismatch")
	}
	if flags&3 == 1 {
		reader, err := zlib.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		body, err = io.ReadAll(io.LimitReader(reader, maxResponse+1))
		reader.Close()
		if err != nil {
			return nil, err
		}
	} else if flags&3 != 2 {
		return nil, errors.New("unsupported native compression")
	}
	if uint64(len(body)) != values[1] {
		return nil, errors.New("native plaintext size mismatch")
	}
	if cookieLen > 0 {
		a.Cookie = append([]byte{}, reply[offset+10:offset+10+cookieLen]...)
	}
	return body, nil
}

type SyncCommand struct {
	ID      int32  `json:"id"`
	Payload []byte `json:"payload"`
}
type SyncBatch struct {
	Cursor   []byte        `json:"cursor"`
	Commands []SyncCommand `json:"commands"`
	Continue bool          `json:"continue"`
}
type Message struct {
	Sender       string `json:"sender,omitempty"`
	LegacyID     uint32 `json:"legacy_id,omitempty"`
	Deleted      bool   `json:"deleted,omitempty"`
	DeleteNotice string `json:"delete_notice,omitempty"`
	RecallTarget string `json:"recall_target_id,omitempty"`
	Hidden       bool   `json:"-"`
	ID           string `json:"id"`
	From         string `json:"from"`
	To           string `json:"to"`
	Peer         string `json:"peer"`
	Type         int32  `json:"type"`
	Text         string `json:"text"`
	Timestamp    uint32 `json:"timestamp"`
	Outgoing     bool   `json:"outgoing"`
}

func decodeCursor(data []byte) (map[uint64]uint64, error) {
	result := map[uint64]uint64{}
	if len(data) == 0 || bytes.Equal(data, []byte{0}) {
		return result, nil
	}
	if len(data) > 65536 {
		return nil, errors.New("native cursor too large")
	}
	fs, err := fields(data)
	if err != nil {
		return nil, err
	}
	count, err := getInt(data, 1)
	if err != nil {
		return nil, err
	}
	for _, f := range fs {
		if f.number != 2 {
			continue
		}
		if f.wire != 2 {
			return nil, errors.New("invalid native cursor entry")
		}
		key, err := getInt(f.data, 1)
		if err != nil {
			return nil, err
		}
		value, err := getInt(f.data, 2)
		if err != nil {
			return nil, err
		}
		if _, found := result[key]; found {
			return nil, errors.New("duplicate native cursor entry")
		}
		result[key] = value
	}
	if uint64(len(result)) != count {
		return nil, errors.New("native cursor count mismatch")
	}
	return result, nil
}
func mergeCursor(prior, incoming []byte, replace bool) ([]byte, error) {
	old, err := decodeCursor(prior)
	if err != nil {
		return nil, err
	}
	next, err := decodeCursor(incoming)
	if err != nil {
		return nil, err
	}
	if replace {
		old = map[uint64]uint64{}
	}
	for key, value := range next {
		if previous, exists := old[key]; !exists || value > previous {
			old[key] = value
		}
	}
	keys := make([]uint64, 0, len(old))
	for key := range old {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	result := pv(1, uint64(len(old)))
	for _, key := range keys {
		result = append(result, pb(2, message(pv(1, key), pv(2, old[key])))...)
	}
	return result, nil
}
func decodeSync(response, prior []byte) (SyncBatch, error) {
	var result SyncBatch
	code, err := getInt(response, 1)
	if err != nil {
		return result, err
	}
	if int32(code) != 0 && int32(code) != -2006 {
		return result, fmt.Errorf("native sync status %d", int32(code))
	}
	list, err := getBytes(response, 2)
	if err != nil {
		return result, err
	}
	fs, err := fields(list)
	if err != nil {
		return result, err
	}
	count, err := getInt(list, 1)
	if err != nil {
		return result, err
	}
	for _, f := range fs {
		if f.number != 2 {
			continue
		}
		if f.wire != 2 {
			return result, errors.New("invalid native sync command")
		}
		id, err := getInt(f.data, 1)
		if err != nil {
			return result, err
		}
		payload, err := optionalBuffer(f.data, 2)
		if err != nil {
			return result, err
		}
		result.Commands = append(result.Commands, SyncCommand{int32(id), payload})
	}
	if count != uint64(len(result.Commands)) {
		return result, errors.New("native sync command count mismatch")
	}
	incoming, err := optionalBuffer(response, 4)
	if err != nil {
		return result, err
	}
	result.Cursor, err = mergeCursor(prior, incoming, int32(code) == -2006)
	if err != nil {
		return result, err
	}
	flags, err := getInt(response, 3)
	if err != nil {
		return result, err
	}
	sent := prior
	if len(sent) == 0 {
		sent = []byte{0}
	}
	result.Continue = flags&7 != 0 && !bytes.Equal(incoming, sent)
	return result, nil
}
func (s *Session) Sync(ctx context.Context, a *Account, cursor []byte) (SyncBatch, error) {
	if len(cursor) == 0 {
		cursor = []byte{0}
	}
	if len(cursor) > 65536 {
		return SyncBatch{}, errors.New("native cursor too large")
	}
	p, err := profileFor(a.Profile)
	if err != nil {
		return SyncBatch{}, err
	}
	request := message(pb(1, pv(1, 0)), pv(2, 7), pb(3, buffer(cursor)), pv(4, 6), pb(5, []byte(p.deviceType)), pv(9, 0))
	response, err := s.authenticated(ctx, a, 121, 138, request)
	if err != nil {
		return SyncBatch{}, err
	}
	return decodeSync(response, cursor)
}
func DecodeMessage(raw []byte, own string) (Message, error) {
	var result Message
	text := func(n uint64) (string, error) {
		nested, err := getBytes(raw, n)
		if err != nil {
			return "", err
		}
		b, err := getBytes(nested, 1)
		return string(b), err
	}
	var err error
	result.From, err = text(2)
	if err != nil {
		return result, err
	}
	result.To, err = text(3)
	if err != nil {
		return result, err
	}
	result.Text, err = text(5)
	if err != nil {
		return result, err
	}
	if own == "" || result.From == "" || result.To == "" || (result.From != own && result.To != own && !strings.HasSuffix(result.From, "@chatroom")) {
		return result, errors.New("native message does not match account")
	}
	result.Outgoing = result.From == own
	result.Peer = result.From
	if result.Outgoing {
		result.Peer = result.To
	}
	v, err := getInt(raw, 4)
	if err != nil {
		return result, err
	}
	result.Type = int32(v)
	v, err = getInt(raw, 9)
	if err != nil || v > 0xffffffff {
		return result, errors.New("invalid native message timestamp")
	}
	result.Timestamp = uint32(v)
	v, err = optionalInt(raw, 12)
	if err != nil {
		return result, err
	}
	if v > 0 {
		result.ID = strconv.FormatUint(v, 10)
	} else {
		hash := md5.Sum(raw)
		result.ID = fmt.Sprintf("local-%x", hash)
	}
	legacy, legacyErr := optionalInt(raw, 1)
	if legacyErr != nil || (legacy > 0xffffffff && (int32(uint32(legacy)) >= 0 || legacy != uint64(int64(int32(uint32(legacy)))))) {
		return result, errors.New("invalid legacy message ID")
	}
	result.LegacyID = uint32(legacy)
	NormalizeMessage(&result)
	return result, nil
}

func (s *Session) SendText(ctx context.Context, a *Account, destination, text string) (string, error) {
	if destination == "" || len(destination) > 256 || strings.ContainsAny(destination, "\x00\r\n") || text == "" || len(text) > 60*1024 {
		return "", errors.New("invalid native text message")
	}
	random, err := randomBytes(4)
	if err != nil {
		return "", err
	}
	id := binary.BigEndian.Uint32(random)
	if id == 0 {
		id = 1
	}
	item := message(pb(1, pb(1, []byte(destination))), pb(2, []byte(text)), pv(3, 1), pv(4, uint64(time.Now().Unix())), pv(5, uint64(id)))
	response, err := s.authenticated(ctx, a, 237, 522, message(pv(1, 1), pb(2, item)))
	if err != nil {
		return "", fmt.Errorf("native send outcome uncertain; do not automatically retry: %w", err)
	}
	return decodeSendReceipt(response, destination, id)
}

func decodeSendReceipt(response []byte, destination string, id uint32) (string, error) {
	if err := requireSuccess(response); err != nil {
		return "", err
	}
	count, err := getInt(response, 2)
	if err != nil || count != 1 {
		return "", errors.New("native send outcome uncertain: unexpected receipt count")
	}
	receipt, err := getBytes(response, 3)
	if err != nil {
		return "", err
	}
	code, err := getInt(receipt, 1)
	if err != nil {
		return "", err
	}
	if int32(code) != 0 {
		return "", fmt.Errorf("native send rejected (status %d)", int32(code))
	}
	to, err := getBytes(receipt, 2)
	if err != nil {
		return "", err
	}
	name, err := getBytes(to, 1)
	if err != nil || string(name) != destination {
		return "", errors.New("native send outcome uncertain: recipient mismatch")
	}
	returned, err := getInt(receipt, 4)
	if err != nil || returned != uint64(id) {
		return "", errors.New("native send outcome uncertain: client ID mismatch")
	}
	serverID, err := getInt(receipt, 8)
	if err != nil || serverID == 0 {
		return "", errors.New("native send outcome uncertain: missing server ID")
	}
	return strconv.FormatUint(serverID, 10), nil
}
