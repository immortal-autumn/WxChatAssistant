package native

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type shortTicket struct {
	kind               byte
	lifetime, reserved uint32
	nonce, opaque      []byte
}

func parseShortTicket(data []byte) (shortTicket, error) {
	var result shortTicket
	top := cursor{data: data}
	body := cursor{data: top.sized(4)}
	if err := top.end(); err != nil {
		return result, err
	}
	if body.number(1) != 4 {
		return result, errors.New("expected native tickets")
	}
	count := body.number(1)
	if count < 1 || count > 4 {
		return result, errors.New("invalid native ticket count")
	}
	for i := uint64(0); i < count; i++ {
		entry := cursor{data: body.sized(4)}
		ticket := shortTicket{kind: byte(entry.number(1)), lifetime: uint32(entry.number(4))}
		entry.sized(2)
		ticket.reserved = uint32(entry.number(4))
		ticket.nonce = append([]byte{}, entry.sized(2)...)
		ticket.opaque = append([]byte{}, entry.sized(2)...)
		if err := entry.end(); err != nil {
			return result, err
		}
		if len(ticket.nonce) != 12 || len(ticket.opaque) == 0 {
			return result, errors.New("invalid native access ticket")
		}
		if i == 0 {
			result = ticket
		}
	}
	return result, body.end()
}
func shortHello(ticket shortTicket) ([]byte, []byte, error) {
	random, err := randomBytes(32)
	if err != nil {
		return nil, nil, err
	}
	now := uint32(time.Now().Unix())
	entry := []byte{ticket.kind}
	entry = binary.BigEndian.AppendUint32(entry, ticket.lifetime)
	entry = append(entry, shortSection(nil)...)
	entry = binary.BigEndian.AppendUint32(entry, ticket.reserved)
	entry = append(entry, shortSection(ticket.nonce)...)
	entry = append(entry, shortSection(ticket.opaque)...)
	extension := section(message([]byte{0, 0x0f, 1}, section(entry)))
	hello := message([]byte{1, 4, 0xf1, 1, 0, 0xa8}, random, binary.BigEndian.AppendUint32(nil, now), section(append([]byte{1}, extension...)))
	extensions := section(message([]byte{8}, section(message([]byte{1}, section(binary.BigEndian.AppendUint32([]byte{0, 0x12}, now))))))
	return section(hello), extensions, nil
}
func decodeShortReply(data, psk, hello, extensions []byte) ([]byte, error) {
	var records [][]byte
	for len(data) > 0 {
		if len(data) < 5 || data[1] != 0xf1 || data[2] != 4 {
			return nil, errors.New("invalid shortlink record")
		}
		n := int(binary.BigEndian.Uint16(data[3:5])) + 5
		if n > len(data) {
			return nil, io.ErrUnexpectedEOF
		}
		records = append(records, data[:n])
		data = data[n:]
	}
	if len(records) < 4 || records[0][0] != 0x16 || records[1][0] != 0x16 || records[len(records)-1][0] != 0x15 {
		return nil, errors.New("invalid shortlink sequence")
	}
	top := cursor{data: records[0][5:]}
	server := cursor{data: top.sized(4)}
	if err := top.end(); err != nil {
		return nil, err
	}
	if server.number(1) != 2 || !bytes.Equal(server.take(2), []byte{4, 0xf1}) || server.number(2) != 0xa8 {
		return nil, errors.New("unsupported shortlink selection")
	}
	server.take(32)
	ext := cursor{data: server.sized(4)}
	if err := server.end(); err != nil {
		return nil, err
	}
	if ext.number(1) != 0 {
		return nil, errors.New("unsupported shortlink extensions")
	}
	if err := ext.end(); err != nil {
		return nil, err
	}
	transcript := message(hello, extensions, records[0][5:])
	keys := expand(psk, append([]byte("handshake key expansion"), digest(transcript)...), 28)
	finishedBody, err := openRecord(records[1], keys[:16], keys[16:], 1)
	if err != nil {
		return nil, err
	}
	finished, err := signedPayload(finishedBody, 0x14)
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(finished, mac(expand(psk, []byte("server finished"), 32), digest(transcript))) {
		return nil, errors.New("shortlink server Finished verification failed")
	}
	var result []byte
	for i, record := range records[2 : len(records)-1] {
		if record[0] != 0x17 {
			return nil, errors.New("unexpected shortlink record")
		}
		plain, err := openRecord(record, keys[:16], keys[16:], uint64(i+2))
		if err != nil {
			return nil, err
		}
		result = append(result, plain...)
		if len(result) > maxResponse {
			return nil, errors.New("shortlink response too large")
		}
	}
	end, err := openRecord(records[len(records)-1], keys[:16], keys[16:], uint64(len(records)-1))
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(end, section([]byte{0, 1, 1})) {
		return nil, errors.New("unexpected shortlink close")
	}
	return result, nil
}
func (s *Session) shortExchange(ctx context.Context, host, path string, payload []byte) ([]byte, error) {
	if !validHost(host) || !strings.HasPrefix(path, "/cgi-bin/micromsg-bin/") || strings.ContainsAny(path, "?\r\n# ") || len(s.accessPSK) != 32 || len(s.shortTicket.nonce) != 12 {
		return nil, errors.New("invalid shortlink request")
	}
	hello, extensions, err := shortHello(s.shortTicket)
	if err != nil {
		return nil, err
	}
	keys := expand(s.accessPSK, append([]byte("early data key expansion"), digest(hello)...), 28)
	route := section(message(shortSection([]byte(path)), shortSection([]byte(host)), section(payload)))
	wire, err := frame(0x19, hello)
	if err != nil {
		return nil, err
	}
	for i, part := range []struct {
		kind byte
		data []byte
	}{{0x19, extensions}, {0x17, route}, {0x15, section([]byte{0, 1, 1})}} {
		record, err := sealRecord(part.kind, part.data, keys[:16], keys[16:], uint64(i+1))
		if err != nil {
			return nil, err
		}
		wire = append(wire, record...)
	}
	nonce, err := randomBytes(4)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+host+"/mmtls/"+hex.EncodeToString(nonce), bytes.NewReader(wire))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("User-Agent", "MicroMessenger Client")
	request.Header.Set("Upgrade", "mmtls")
	request.Header.Set("Cache-Control", "no-cache")
	request.Close = true
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("native shortlink HTTP status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponse {
		return nil, errors.New("shortlink receive budget exceeded")
	}
	return decodeShortReply(data, s.accessPSK, hello, extensions)
}
func (a *Account) shortHost() string { return strings.Replace(a.Host, "long", "short", 1) }
