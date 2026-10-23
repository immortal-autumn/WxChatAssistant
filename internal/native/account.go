package native

import (
	"context"
	"crypto/elliptic"
	"crypto/md5"
	"crypto/rand"
	"errors"
	"fmt"
)

// Account is secret native authorization state. It must not be printed. It is
// unrelated to the iLink token and never loads a desktop app's private files.
type Account struct {
	Host        string `json:"host"`
	Device      []byte `json:"device"`
	UIN         uint32 `json:"uin"`
	Username    string `json:"username"`
	Cookie      []byte `json:"cookie"`
	SessionKey  []byte `json:"session_key"`
	RequestKey  []byte `json:"request_key"`
	ResponseKey []byte `json:"response_key"`
	AutoAuthKey []byte `json:"auto_auth_key,omitempty"`
	KeyInfo     []byte `json:"key_info,omitempty"`
}

func (a *Account) Validate() error {
	if a == nil || !validHost(a.Host) || len(a.Device) != 16 || a.UIN == 0 || a.Username == "" || len(a.Username) > 512 || len(a.Cookie) > 15 || len(a.RequestKey) != 24 || len(a.ResponseKey) != 24 {
		return errors.New("invalid or unsupported native session")
	}
	return nil
}

func nativeRedirect(response []byte) (string, error) {
	network, err := getBytes(response, 5)
	if err != nil {
		return "", err
	}
	hosts, err := getBytes(network, 1)
	if err != nil {
		return "", err
	}
	fs, err := fields(hosts)
	if err != nil {
		return "", err
	}
	var host string
	for _, f := range fs {
		if f.number != 2 {
			continue
		}
		if f.wire != 2 {
			return "", errors.New("invalid native redirect")
		}
		source, err := getBytes(f.data, 1)
		if err != nil {
			return "", err
		}
		if string(source) != "long.weixin.qq.com" {
			continue
		}
		if host != "" {
			return "", errors.New("ambiguous native redirect")
		}
		target, err := getBytes(f.data, 2)
		if err != nil {
			return "", err
		}
		host = string(target)
	}
	if !validHost(host) {
		return "", errors.New("untrusted native redirect")
	}
	return host, nil
}
func (s *Session) Authenticate(ctx context.Context, attempt *LoginAttempt) (*Account, error) {
	if attempt == nil || attempt.Status != 2 || len(attempt.Credential3) == 0 || len(attempt.Credential4) == 0 {
		return nil, errors.New("native QR has not been approved")
	}
	private, x, y, err := elliptic.GenerateKey(elliptic.P224(), rand.Reader)
	if err != nil {
		return nil, err
	}
	defer clear(private)
	public := elliptic.Marshal(elliptic.P224(), x, y)
	key, err := randomBytes(16)
	if err != nil {
		return nil, err
	}
	rsa := message(pb(1, buffer(key)), pb(2, message(pv(1, 713), pb(2, buffer(public)))), pb(3, attempt.Credential3), pb(4, attempt.Credential4), pb(5, nil))
	wt := message(pb(1, nil), pb(4, buffer([]byte{0})))
	aes := message(pb(1, baseRequest(attempt.Device, 0, 1)), pb(2, pb(2, wt)), pv(5, 0), pb(8, []byte("WeChat CLI")), pb(9, []byte(deviceType)), pb(11, nil), pv(13, 1000), pb(19, []byte("0")), pv(22, 2))
	request := message(pb(1, rsa), pb(2, aes))
	for redirects := 0; redirects < 2; redirects++ {
		response, cookie, err := s.prelogin(ctx, 433, 252, request, attempt.Cookie)
		if err != nil {
			return nil, err
		}
		code, err := baseCode(response)
		if err != nil {
			return nil, err
		}
		if code == -301 && redirects == 0 {
			host, err := nativeRedirect(response)
			if err != nil {
				return nil, err
			}
			next, err := Connect(ctx, host)
			if err != nil {
				return nil, err
			}
			s.Close()
			*s = *next
			attempt.Host = host
			attempt.Cookie = cookie
			continue
		}
		if code != 0 {
			return nil, fmt.Errorf("native account authentication rejected (status %d); not retried", code)
		}
		account, err := decodeAccount(response, private, attempt.Device, attempt.Host, cookie)
		if err != nil {
			return nil, err
		}
		clear(attempt.Credential3)
		clear(attempt.Credential4)
		attempt.Credential3 = nil
		attempt.Credential4 = nil
		return account, nil
	}
	return nil, errors.New("native authentication redirect limit")
}

func decodeAccount(response, private, device []byte, host string, cookie []byte) (*Account, error) {
	mask, err := getInt(response, 2)
	if err != nil {
		return nil, err
	}
	if mask&1 == 0 {
		return nil, errors.New("native authentication has no session key section")
	}
	auth, err := getBytes(response, 3)
	if err != nil {
		return nil, err
	}
	account, err := getBytes(response, 4)
	if err != nil {
		return nil, err
	}
	uin, err := getInt(auth, 1)
	if err != nil || uin > 0xffffffff {
		return nil, errors.New("invalid native account UIN")
	}
	username, err := getBytes(account, 1)
	if err != nil {
		return nil, err
	}
	key, err := optionalBuffer(auth, 3)
	if err != nil {
		return nil, err
	}
	flags, err := optionalInt(auth, 19)
	if err != nil {
		return nil, err
	}
	if flags&4 != 0 {
		ecdh, err := getBytes(auth, 2)
		if err != nil {
			return nil, err
		}
		curve, err := getInt(ecdh, 1)
		if err != nil || curve != 713 {
			return nil, errors.New("unsupported native account curve")
		}
		point, err := optionalBuffer(ecdh, 2)
		if err != nil {
			return nil, err
		}
		x, y := elliptic.Unmarshal(elliptic.P224(), point)
		if x == nil {
			return nil, errors.New("invalid native account public key")
		}
		sx, _ := elliptic.P224().ScalarMult(x, y, private)
		shared := sx.FillBytes(make([]byte, 28))
		hash := md5.Sum(shared)
		clear(shared)
		key, err = decryptCBC(key, hash[:])
		clear(hash[:])
		if err != nil {
			return nil, err
		}
	}
	requestKey, err := optionalBuffer(auth, 23)
	if err != nil {
		return nil, err
	}
	responseKey, err := optionalBuffer(auth, 24)
	if err != nil {
		return nil, err
	}
	auto, err := optionalBuffer(auth, 4)
	if err != nil {
		return nil, err
	}
	info, err := optionalBuffer(auth, 10)
	if err != nil {
		return nil, err
	}
	result := &Account{Host: host, Device: device, UIN: uint32(uin), Username: string(username), Cookie: cookie, SessionKey: key, RequestKey: requestKey, ResponseKey: responseKey, AutoAuthKey: auto, KeyInfo: info}
	return result, result.Validate()
}
