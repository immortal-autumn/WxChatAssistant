// Package native contains experimental desktop-protocol probes, separate from
// the iLink bot client. It does not load or launch the official application.
package native

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const maxResponse = 2 << 20

// Public verification pins for the Linux 4.1.13.26 protocol profile. These are
// server public keys, not account credentials. See docs/native-protocol.md.
var serverPins = map[uint32]string{
	5:    "-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEvDq9RQg9ho32ctJGZ1+KtEFksBSF\nEECOCnuhqRPPXQtmvUrUtVvfIdFommkrg/Iyd8vYMdDdtO8GkeoCkJ6FBA==\n-----END PUBLIC KEY-----",
	1000: "-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEvNO5sjEzAoQpX1WVHmJEM7sNn4d5\nGRix7PLXtaU+wrQ5LgCEwxu6s7hcEUHMMTc9Tc2I+1cuJMaLgST3Q3yKXg==\n-----END PUBLIC KEY-----",
}

type cursor struct {
	data   []byte
	offset int
	err    error
}

func (c *cursor) take(n int) []byte {
	if c.err != nil {
		return nil
	}
	if n < 0 || n > len(c.data)-c.offset {
		c.err = errors.New("truncated native structure")
		return nil
	}
	b := c.data[c.offset : c.offset+n]
	c.offset += n
	return b
}
func (c *cursor) number(n int) uint64 {
	var v uint64
	for _, b := range c.take(n) {
		v = v<<8 | uint64(b)
	}
	return v
}
func (c *cursor) sized(n int) []byte {
	size := c.number(n)
	if size > maxResponse {
		c.err = errors.New("native structure too large")
		return nil
	}
	return c.take(int(size))
}
func (c *cursor) end() error {
	if c.err != nil {
		return c.err
	}
	if c.offset != len(c.data) {
		return errors.New("unexpected native structure extension")
	}
	return nil
}
func section(b []byte) []byte {
	return append(binary.BigEndian.AppendUint32(nil, uint32(len(b))), b...)
}
func shortSection(b []byte) []byte {
	return append(binary.BigEndian.AppendUint16(nil, uint16(len(b))), b...)
}
func digest(b []byte) []byte   { s := sha256.Sum256(b); return s[:] }
func mac(key, b []byte) []byte { h := hmac.New(sha256.New, key); h.Write(b); return h.Sum(nil) }
func expand(key, info []byte, n int) []byte {
	var out, last []byte
	for counter := byte(1); len(out) < n; counter++ {
		input := append(append(append([]byte{}, last...), info...), counter)
		last = mac(key, input)
		out = append(out, last...)
	}
	return out[:n]
}

type traffic struct{ clientKey, serverKey, clientIV, serverIV []byte }

func deriveTraffic(secret []byte, label string, transcript []byte) traffic {
	b := expand(secret, append([]byte(label), digest(transcript)...), 56)
	return traffic{b[:16], b[16:32], b[32:44], b[44:56]}
}
func recordNonce(iv []byte, sequence uint64) ([]byte, error) {
	if len(iv) != 12 {
		return nil, errors.New("invalid native IV")
	}
	n := append([]byte{}, iv...)
	seq := binary.BigEndian.AppendUint64(nil, sequence)
	for i, b := range seq {
		n[i+4] ^= b
	}
	return n, nil
}
func frame(kind byte, body []byte) ([]byte, error) {
	if len(body) > 65535 {
		return nil, errors.New("native record too large")
	}
	header := binary.BigEndian.AppendUint16([]byte{kind, 0xf1, 0x04}, uint16(len(body)))
	return append(header, body...), nil
}
func sealRecord(kind byte, plain, key, iv []byte, sequence uint64) ([]byte, error) {
	if len(plain) > 65519 {
		return nil, errors.New("native plaintext too large")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, err := recordNonce(iv, sequence)
	if err != nil {
		return nil, err
	}
	header := binary.BigEndian.AppendUint16([]byte{kind, 0xf1, 0x04}, uint16(len(plain)+16))
	aad := append(binary.BigEndian.AppendUint64(nil, sequence), header...)
	return append(header, gcm.Seal(nil, nonce, plain, aad)...), nil
}
func openRecord(record, key, iv []byte, sequence uint64) ([]byte, error) {
	if len(record) < 21 || record[1] != 0xf1 || record[2] != 0x04 || int(binary.BigEndian.Uint16(record[3:5])) != len(record)-5 {
		return nil, errors.New("invalid native encrypted record")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, err := recordNonce(iv, sequence)
	if err != nil {
		return nil, err
	}
	aad := append(binary.BigEndian.AppendUint64(nil, sequence), record[:5]...)
	return gcm.Open(nil, nonce, record[5:], aad)
}

type serverHello struct {
	keyID, region, version uint32
	point                  []byte
}

func parseHello(data []byte) (serverHello, error) {
	var result serverHello
	top := cursor{data: data}
	body := cursor{data: top.sized(4)}
	if err := top.end(); err != nil {
		return result, err
	}
	if body.number(1) != 2 || !bytes.Equal(body.take(2), []byte{4, 0xf1}) || body.number(2) != 0xc02b {
		return result, errors.New("unsupported native ServerHello")
	}
	body.take(32)
	extensions := cursor{data: body.sized(4)}
	if err := body.end(); err != nil {
		return result, err
	}
	count := extensions.number(1)
	seen := map[uint64]bool{}
	for range count {
		x := cursor{data: extensions.sized(4)}
		kind := x.number(2)
		if seen[kind] {
			return result, errors.New("duplicate native extension")
		}
		seen[kind] = true
		switch kind {
		case 0x11:
			result.keyID = uint32(x.number(4))
			result.point = x.sized(2)
			if len(result.point) != 65 || result.point[0] != 4 {
				return result, errors.New("invalid native key share")
			}
			if err := x.end(); err != nil {
				return result, err
			}
		case 0x13:
			result.region = uint32(x.number(4))
			result.version = uint32(x.number(4))
			if err := x.end(); err != nil {
				return result, err
			}
		}
	}
	if err := extensions.end(); err != nil {
		return result, err
	}
	if !seen[0x11] || !seen[0x13] {
		return result, errors.New("missing native handshake extension")
	}
	return result, nil
}
func signedPayload(data []byte, kind byte) ([]byte, error) {
	top := cursor{data: data}
	body := cursor{data: top.sized(4)}
	if err := top.end(); err != nil {
		return nil, err
	}
	if body.number(1) != uint64(kind) {
		return nil, errors.New("unexpected native handshake message")
	}
	value := body.sized(2)
	return value, body.end()
}
func verifySignature(public string, data, signature []byte) error {
	block, _ := pem.Decode([]byte(public))
	if block == nil {
		return errors.New("missing native server pin")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return err
	}
	key, ok := parsed.(*ecdsa.PublicKey)
	if !ok || !ecdsa.VerifyASN1(key, digest(data), signature) {
		return errors.New("native server signature verification failed")
	}
	return nil
}
func validHost(host string) bool {
	if !(strings.HasSuffix(host, ".weixin.qq.com") || strings.HasSuffix(host, ".wechat.com")) {
		return false
	}
	for _, r := range host {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}

// Session is an account-free authenticated connection to the native service.
// It holds ephemeral traffic keys only and is never serialized.
type Session struct {
	conn                       net.Conn
	traffic                    traffic
	outgoing, incoming         uint64
	received                   int
	Region, CertificateVersion uint32
	pending                    []byte
	rpcSequence                uint32
}

func (s *Session) Close() error { return s.conn.Close() }
func (s *Session) read(kind byte) ([]byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(s.conn, header); err != nil {
		return nil, err
	}
	if header[0] != kind || header[1] != 0xf1 || header[2] != 4 {
		return nil, errors.New("unexpected native record header")
	}
	n := int(binary.BigEndian.Uint16(header[3:]))
	s.received += 5 + n
	if s.received > maxResponse {
		return nil, errors.New("native receive budget exceeded")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(s.conn, body); err != nil {
		return nil, err
	}
	return append(header, body...), nil
}
func (s *Session) write(data []byte) error {
	for len(data) > 0 {
		n, err := s.conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func Connect(ctx context.Context, host string) (_ *Session, err error) {
	if !validHost(host) {
		return nil, errors.New("unsupported native service hostname")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, "80"))
	if err != nil {
		return nil, err
	}
	s := &Session{conn: conn}
	defer func() {
		if err != nil {
			conn.Close()
		}
	}()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	var keys [2]*ecdh.PrivateKey
	var shares []byte
	for i := range keys {
		keys[i], err = ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		share := binary.BigEndian.AppendUint32(nil, uint32(i+5))
		share = append(share, shortSection(keys[i].PublicKey().Bytes())...)
		shares = append(shares, section(share)...)
	}
	extra := []byte{0, 0, 0, 0, 2}
	extra = binary.BigEndian.AppendUint32(extra, 5)
	extra = binary.BigEndian.AppendUint32(extra, 1000)
	extension := section(append(append([]byte{0, 0x10, 2}, shares...), extra...))
	random := make([]byte, 32)
	if _, err = rand.Read(random); err != nil {
		return nil, err
	}
	hello := append([]byte{1, 4, 0xf1, 1, 0xc0, 0x2b}, random...)
	hello = binary.BigEndian.AppendUint32(hello, uint32(time.Now().Unix()))
	hello = section(append(hello, section(append([]byte{1}, extension...))...))
	framed, err := frame(0x16, hello)
	if err != nil {
		return nil, err
	}
	if err = s.write(framed); err != nil {
		return nil, err
	}
	record, err := s.read(0x16)
	if err != nil {
		return nil, err
	}
	peer, err := parseHello(record[5:])
	if err != nil {
		return nil, err
	}
	if peer.keyID < 5 || peer.keyID > 6 || serverPins[peer.version] == "" || peer.region < 1 || peer.region > 2 {
		return nil, errors.New("unsupported native server selection")
	}
	point, err := ecdh.P256().NewPublicKey(peer.point)
	if err != nil {
		return nil, err
	}
	shared, err := keys[peer.keyID-5].ECDH(point)
	if err != nil {
		return nil, err
	}
	secret := digest(bytes.TrimLeft(shared, "\x00"))
	transcript := append(append([]byte{}, hello...), record[5:]...)
	handshake := deriveTraffic(secret, "handshake key expansion", transcript)
	record, err = s.read(0x16)
	if err != nil {
		return nil, err
	}
	signatureBody, err := openRecord(record, handshake.serverKey, handshake.serverIV, 1)
	if err != nil {
		return nil, err
	}
	signature, err := signedPayload(signatureBody, 0x0f)
	if err != nil {
		return nil, err
	}
	if err = verifySignature(serverPins[peer.version], digest(transcript), signature); err != nil {
		return nil, err
	}
	transcript = append(transcript, signatureBody...)
	record, err = s.read(0x16)
	if err != nil {
		return nil, err
	}
	ticket, err := openRecord(record, handshake.serverKey, handshake.serverIV, 2)
	if err != nil {
		return nil, err
	}
	top := cursor{data: ticket}
	ticketBody := cursor{data: top.sized(4)}
	if ticketBody.number(1) != 4 {
		return nil, errors.New("unexpected native ticket")
	}
	if err = top.end(); err != nil {
		return nil, err
	}
	transcript = append(transcript, ticket...)
	record, err = s.read(0x16)
	if err != nil {
		return nil, err
	}
	finishedBody, err := openRecord(record, handshake.serverKey, handshake.serverIV, 3)
	if err != nil {
		return nil, err
	}
	finished, err := signedPayload(finishedBody, 0x14)
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(finished, mac(expand(secret, []byte("server finished"), 32), digest(transcript))) {
		return nil, errors.New("native server Finished verification failed")
	}
	clientFinished := mac(expand(secret, []byte("client finished"), 32), digest(transcript))
	finish := section(append([]byte{0x14}, shortSection(clientFinished)...))
	framed, err = sealRecord(0x16, finish, handshake.clientKey, handshake.clientIV, 1)
	if err != nil {
		return nil, err
	}
	if err = s.write(framed); err != nil {
		return nil, err
	}
	expanded := expand(secret, append([]byte("expanded secret"), digest(transcript)...), 32)
	s.traffic = deriveTraffic(expanded, "application data key expansion", transcript)
	s.Region = peer.region
	s.CertificateVersion = peer.version
	s.outgoing = 2
	s.incoming = 4
	if err = s.send(6, ^uint32(0), nil); err != nil {
		return nil, err
	}
	command, sequence, payload, err := s.receive()
	if err != nil {
		return nil, err
	}
	if command != 1000000006 || sequence != ^uint32(0) || len(payload) != 0 {
		return nil, errors.New("unexpected native NOOP response")
	}
	return s, nil
}
func (s *Session) send(command, sequence uint32, payload []byte) error {
	if len(payload) > 65503 {
		return errors.New("native longlink payload too large")
	}
	packet := binary.BigEndian.AppendUint32(nil, uint32(len(payload)+16))
	packet = append(packet, 0, 16, 0, 1)
	packet = binary.BigEndian.AppendUint32(packet, command)
	packet = binary.BigEndian.AppendUint32(packet, sequence)
	packet = append(packet, payload...)
	record, err := sealRecord(0x17, packet, s.traffic.clientKey, s.traffic.clientIV, s.outgoing)
	if err != nil {
		return err
	}
	if err = s.write(record); err != nil {
		return err
	}
	s.outgoing++
	return nil
}
func (s *Session) receive() (uint32, uint32, []byte, error) {
	for {
		if len(s.pending) >= 16 {
			n := int(binary.BigEndian.Uint32(s.pending))
			if n < 16 || n > maxResponse {
				return 0, 0, nil, errors.New("invalid native longlink size")
			}
			if len(s.pending) >= n {
				p := s.pending[:n]
				if !bytes.Equal(p[4:8], []byte{0, 16, 0, 1}) {
					return 0, 0, nil, errors.New("unsupported native longlink header")
				}
				command, sequence := binary.BigEndian.Uint32(p[8:12]), binary.BigEndian.Uint32(p[12:16])
				payload := append([]byte{}, p[16:]...)
				s.pending = s.pending[n:]
				return command, sequence, payload, nil
			}
		}
		record, err := s.read(0x17)
		if err != nil {
			return 0, 0, nil, err
		}
		plain, err := openRecord(record, s.traffic.serverKey, s.traffic.serverIV, s.incoming)
		if err != nil {
			return 0, 0, nil, err
		}
		s.incoming++
		s.pending = append(s.pending, plain...)
	}
}

func (s *Session) String() string {
	return fmt.Sprintf("MMTLS authenticated (region %d, certificate %d)", s.Region, s.CertificateVersion)
}

func (s *Session) rpc(ctx context.Context, command uint32, payload []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	if err := s.conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { s.conn.Close() })
	defer stop()
	s.received = 0
	s.rpcSequence++
	if s.rpcSequence == 0 {
		return nil, errors.New("native request sequence exhausted")
	}
	if err := s.send(command, s.rpcSequence, payload); err != nil {
		return nil, err
	}
	for range 32 {
		c, seq, response, err := s.receive()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		if seq == s.rpcSequence && (c == command || c == command+1000000000) {
			return response, nil
		}
	}
	return nil, errors.New("too many unrelated native notifications")
}
