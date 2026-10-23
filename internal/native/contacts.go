package native

import "errors"

// Contact is the subset of a native ModContact needed to identify recipients.
type Contact struct {
	Username string `json:"username"`
	Nickname string `json:"nickname,omitempty"`
	Remark   string `json:"remark,omitempty"`
	Alias    string `json:"alias,omitempty"`
}

func DecodeContact(raw []byte) (Contact, error) {
	var c Contact
	sk := func(n uint64) (string, error) {
		b, err := optionalBytes(raw, n)
		if err != nil || len(b) == 0 {
			return "", err
		}
		s, err := optionalBytes(b, 1)
		return string(s), err
	}
	var err error
	c.Username, err = sk(1)
	if err != nil {
		return c, err
	}
	if c.Username == "" {
		return c, errors.New("contact has no username")
	}
	c.Nickname, err = sk(2)
	if err != nil {
		return c, err
	}
	c.Remark, err = sk(10)
	if err != nil {
		return c, err
	}
	alias, err := optionalBytes(raw, 30)
	c.Alias = string(alias)
	return c, err
}
