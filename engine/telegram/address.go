package telegram

import (
	"strconv"
	"strings"
)

// A Telegram account's address in the tables it shares with WhatsApp
// (chat sessions, channel languages): tg:<user id>, where a WhatsApp
// number is +<E.164>, so the two never collide (00175).
const addrPrefix = "tg:"

// Address is a Telegram user id's address.
func Address(id int64) string { return addrPrefix + strconv.FormatInt(id, 10) }

// ParseAddress reads a tg: address: a positive user id in canonical form.
func ParseAddress(addr string) (int64, bool) {
	s, ok := strings.CutPrefix(addr, addrPrefix)
	if !ok || s == "" || len(s) > 20 || s[0] < '1' || s[0] > '9' {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// IsAddress reports whether addr is a Telegram account's address.
func IsAddress(addr string) bool {
	_, ok := ParseAddress(addr)
	return ok
}
