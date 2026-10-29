package native

import (
	"errors"
	"strings"
)

// Contact is the subset of a native ModContact needed to identify recipients.
type Contact struct {
	Members        map[string]string `json:"members,omitempty"`
	Username       string            `json:"username"`
	Nickname       string            `json:"nickname,omitempty"`
	Remark         string            `json:"remark,omitempty"`
	Alias          string            `json:"alias,omitempty"`
	Flags          uint32            `json:"flags,omitempty"`
	FlagMask       uint32            `json:"flag_mask,omitempty"`
	VerifyFlag     *uint32           `json:"verify_flag,omitempty"`
	ChatRoomNotify *uint32           `json:"chatroom_notify,omitempty"`
	notifyOnly     bool              // A command 20 delta must not replace names or pin settings.
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
	mask, err := optionalInt(raw, 7)
	if err != nil {
		return c, err
	}
	flags, err := optionalInt(raw, 8)
	if err != nil {
		return c, err
	}
	c.FlagMask, c.Flags = uint32(mask), uint32(flags)
	alias, err := optionalBytes(raw, 30)
	c.Alias = string(alias)
	if err != nil {
		return c, err
	}
	c.VerifyFlag, err = contactOptionalUint32(raw, 24)
	if err != nil {
		return c, err
	}
	c.ChatRoomNotify, err = contactOptionalUint32(raw, 17)
	if err != nil {
		return c, err
	}
	c.Members = contactRoomMembers(raw)
	return c, err
}

func (c Contact) Pinned() bool { return c.Flags&0x800 != 0 }

func (c Contact) Group() bool { return strings.HasSuffix(c.Username, "@chatroom") }

func (c Contact) Official() bool {
	return !c.Group() && (strings.HasPrefix(c.Username, "gh_") || (c.VerifyFlag != nil && *c.VerifyFlag&8 != 0))
}

// Missing or unknown notification values are not evidence that a chat is muted.
func (c Contact) Muted() bool {
	return c.Group() && c.ChatRoomNotify != nil && *c.ChatRoomNotify == 0
}

func contactOptionalUint32(raw []byte, number uint64) (*uint32, error) {
	fs, err := fields(raw)
	if err != nil {
		return nil, err
	}
	for _, f := range fs {
		if f.number == number {
			v, err := getInt(raw, number)
			if err != nil {
				return nil, err
			}
			if v > 1<<32-1 {
				return nil, errors.New("contact property exceeds uint32")
			}
			n := uint32(v)
			return &n, nil
		}
	}
	return nil, nil
}

// MergeContact combines updates for the same contact. Optional classification
// properties survive updates that omit them; an explicit zero still applies.
func MergeContact(previous, update Contact) Contact {
	if update.notifyOnly {
		if previous.Username == "" {
			return update
		}
		previous.ChatRoomNotify = update.ChatRoomNotify
		return previous
	}
	update.Flags = (previous.Flags &^ update.FlagMask) | (update.Flags & update.FlagMask)
	update.FlagMask |= previous.FlagMask
	if len(update.Members) == 0 {
		update.Members = previous.Members
	}
	if update.VerifyFlag == nil {
		update.VerifyFlag = previous.VerifyFlag
	}
	if update.ChatRoomNotify == nil {
		update.ChatRoomNotify = previous.ChatRoomNotify
	}
	return update
}

// DecodeChatRoomNotify reads sync command 20 (ModChatRoomNotify). Its room ID is
// an SKBuiltinString_t in field 1; field 2 is the notification status.
func DecodeChatRoomNotify(raw []byte) (Contact, error) {
	c := Contact{notifyOnly: true}
	room, err := getBytes(raw, 1)
	if err != nil {
		return c, err
	}
	username, err := getBytes(room, 1)
	if err != nil {
		return c, err
	}
	c.Username = string(username)
	if !c.Group() {
		return c, errors.New("notification update has no group ID")
	}
	c.ChatRoomNotify, err = contactOptionalUint32(raw, 2)
	if err == nil && c.ChatRoomNotify == nil {
		err = errors.New("notification update has no status")
	}
	return c, err
}
