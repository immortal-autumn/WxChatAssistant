package native

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Public business-layer pins for the same desktop profile as the MMTLS pins.
var businessPins = map[uint32]struct {
	version          uint64
	point, signature string
}{
	1: {10014, "0485140cd4892095aecbf79e71cef6348a92eb9b3bbc697e38ab11bd1187cca8e749623dc33c94e0d5166c6de42018258edaf6128ec08a9548927e60c29ed46116", "-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAECh6rXlsE2tI0kPePrkGdqfax3fM4\neqr7vmerVCTDizLW9FVby/9Uz3wxV5IQXjBT9JnLSNrhwPG/CbufQGwMvQ==\n-----END PUBLIC KEY-----"},
	2: {50005, "0476a400ef255ef6729aa837be190b3d30832deb78e4fad786c2e94ecc6b0e8ca69f8f585726c6422bb953067edbf2e907918716ba55175d4e2c167c41aa709558", "-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEKayuW6LOC4vQ74qVRu25yeDLP6v2\nSqBIIdoY7iFk2k89NLjCAST9imZBpPUgHGeyDVWBvgIRHDoVW3zAfAWTKA==\n-----END PUBLIC KEY-----"},
}

func pv(field uint64, value uint64) []byte {
	return binary.AppendUvarint(binary.AppendUvarint(nil, field<<3), value)
}
func pb(field uint64, value []byte) []byte {
	return append(binary.AppendUvarint(binary.AppendUvarint(nil, field<<3|2), uint64(len(value))), value...)
}
func message(fields ...[]byte) []byte { return bytes.Join(fields, nil) }
func buffer(value []byte) []byte      { return message(pv(1, uint64(len(value))), pb(2, value)) }

type field struct {
	number, scalar uint64
	data           []byte
	wire           byte
}

func fields(data []byte) ([]field, error) {
	if len(data) > maxResponse {
		return nil, errors.New("native protobuf too large")
	}
	var result []field
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 || tag>>3 == 0 {
			return nil, errors.New("invalid native protobuf tag")
		}
		data = data[n:]
		f := field{number: tag >> 3, wire: byte(tag & 7)}
		switch f.wire {
		case 0:
			f.scalar, n = binary.Uvarint(data)
			if n <= 0 {
				return nil, errors.New("invalid native protobuf integer")
			}
			data = data[n:]
		case 1:
			if len(data) < 8 {
				return nil, io.ErrUnexpectedEOF
			}
			data = data[8:]
		case 2:
			var length uint64
			length, n = binary.Uvarint(data)
			if n <= 0 || length > uint64(len(data)-n) {
				return nil, errors.New("invalid native protobuf bytes")
			}
			f.data = data[n : n+int(length)]
			data = data[n+int(length):]
		case 5:
			if len(data) < 4 {
				return nil, io.ErrUnexpectedEOF
			}
			data = data[4:]
		default:
			return nil, errors.New("unsupported native protobuf wire type")
		}
		result = append(result, f)
	}
	return result, nil
}
func getField(data []byte, number uint64, wire byte) (field, error) {
	f, err := fields(data)
	if err != nil {
		return field{}, err
	}
	var result field
	found := false
	for _, v := range f {
		if v.number == number {
			if found || v.wire != wire {
				return field{}, errors.New("ambiguous native protobuf field")
			}
			result = v
			found = true
		}
	}
	if !found {
		return field{}, fmt.Errorf("missing native protobuf field %d", number)
	}
	return result, nil
}
func getBytes(data []byte, number uint64) ([]byte, error) {
	f, err := getField(data, number, 2)
	return f.data, err
}
func getInt(data []byte, number uint64) (uint64, error) {
	f, err := getField(data, number, 0)
	return f.scalar, err
}
func unbuffer(data []byte) ([]byte, error) {
	b, err := optionalBytes(data, 2)
	if err != nil {
		return nil, err
	}
	n, err := getInt(data, 1)
	if err != nil || n != uint64(len(b)) {
		return nil, errors.New("invalid native buffer size")
	}
	return b, nil
}
func randomBytes(n int) ([]byte, error) { b := make([]byte, n); _, err := rand.Read(b); return b, err }
func sealEnvelope(plain, key, aad []byte) ([]byte, error) {
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(plain); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, err := randomBytes(12)
	if err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, compressed.Bytes(), aad)
	n := len(sealed) - 16
	return message(sealed[:n], nonce, sealed[n:]), nil
}
func openEnvelope(data, key, aad []byte) ([]byte, error) {
	if len(data) < 28 {
		return nil, errors.New("short native business envelope")
	}
	n := len(data) - 28
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, data[n:n+12], message(data[:n], data[n+12:]), aad)
	if err != nil {
		return nil, err
	}
	reader, err := zlib.NewReader(bytes.NewReader(plain))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	result, err := io.ReadAll(io.LimitReader(reader, maxResponse+1))
	if len(result) > maxResponse {
		return nil, errors.New("native business response too large")
	}
	return result, err
}

// prelogin performs a signed native business exchange. As in the successful
// account-free probe, notice checksums are currently zero; rejection is final.
func (s *Session) prelogin(ctx context.Context, command uint32, cgi uint64, plain, cookie []byte) ([]byte, []byte, error) {
	pin, ok := businessPins[s.Region]
	if !ok {
		return nil, nil, errors.New("unsupported native business region")
	}
	if len(cookie) > 15 {
		return nil, nil, errors.New("native cookie too large")
	}
	local, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	public := local.PublicKey().Bytes()
	point, err := hex.DecodeString(pin.point)
	if err != nil {
		return nil, nil, err
	}
	peer, err := ecdh.P256().NewPublicKey(point)
	if err != nil {
		return nil, nil, err
	}
	shared, err := local.ECDH(peer)
	if err != nil {
		return nil, nil, err
	}
	seed, err := randomBytes(32)
	if err != nil {
		return nil, nil, err
	}
	prefix := message([]byte("1415"), public)
	token, err := sealEnvelope(seed, digest(shared)[:24], digest(prefix))
	if err != nil {
		return nil, nil, err
	}
	derived := expand(mac([]byte("security hdkf expand"), seed), digest(prefix), 56)
	encrypted, err := sealEnvelope(plain, derived[:24], digest(message(prefix, token)))
	if err != nil {
		return nil, nil, err
	}
	request := message(pv(1, 1), pb(2, message(pv(1, 415), pb(2, public))), pb(3, token), pb(4, nil), pb(5, encrypted))
	header := make([]byte, 8)
	header = append(header, cookie...)
	for _, v := range []uint64{uint64(cgi), uint64(len(plain)), uint64(len(plain)), pin.version, 0, 0} {
		header = binary.AppendUvarint(header, v)
	}
	header = append(header, 0xff, 0, 0, 0, 0, 0, 1)
	n := len(header) + 3
	if n > 63 {
		return nil, nil, errors.New("native business header too large")
	}
	flags := uint16(n<<2 | 2 | 12<<12 | len(cookie)<<8)
	packet := message([]byte{0xbf, byte(flags), byte(flags >> 8)}, header, request)
	reply, err := s.rpc(ctx, command, packet)
	if err != nil {
		return nil, nil, err
	}
	if len(reply) < 11 {
		return nil, nil, errors.New("short native QR business response")
	}
	offset := 0
	if reply[0] == 0xbf {
		offset = 1
	}
	flags = binary.LittleEndian.Uint16(reply[offset : offset+2])
	boundary := int(flags >> 2 & 63)
	code := int32(binary.BigEndian.Uint32(reply[offset+2 : offset+6]))
	if code != 0 {
		return nil, nil, fmt.Errorf("native QR business status %d", code)
	}
	if boundary < offset+10+int(flags>>8&15) || boundary > len(reply) {
		return nil, nil, errors.New("invalid native business boundary")
	}
	hybrid := reply[boundary:]
	remoteKey, err := getBytes(hybrid, 1)
	if err != nil {
		return nil, nil, err
	}
	curve, err := getInt(remoteKey, 1)
	if err != nil || curve != 415 {
		return nil, nil, errors.New("unexpected native business curve")
	}
	remotePoint, err := getBytes(remoteKey, 2)
	if err != nil {
		return nil, nil, err
	}
	credential, err := getInt(hybrid, 2)
	if err != nil || credential != 1 {
		return nil, nil, errors.New("unexpected native business credential")
	}
	ciphertext, err := getBytes(hybrid, 3)
	if err != nil {
		return nil, nil, err
	}
	signature, err := getBytes(hybrid, 4)
	if err != nil {
		return nil, nil, err
	}
	if err = verifySignature(pin.signature, digest(message(encrypted, ciphertext)), signature); err != nil {
		return nil, nil, err
	}
	remote, err := ecdh.P256().NewPublicKey(remotePoint)
	if err != nil {
		return nil, nil, err
	}
	shared, err = local.ECDH(remote)
	if err != nil {
		return nil, nil, err
	}
	response, err := openEnvelope(ciphertext, digest(shared)[:24], digest(message(derived[24:], plain, []byte("415"), remotePoint, []byte("1"))))
	if err != nil {
		return nil, nil, err
	}
	return response, append([]byte{}, reply[offset+10:offset+10+int(flags>>8&15)]...), nil
}
