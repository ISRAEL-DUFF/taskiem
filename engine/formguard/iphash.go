package formguard

import (
	"crypto/hmac"
	"crypto/sha256"
	"net/netip"
	"strings"
	"time"
)

// HashIP is a keyed hash of a client's address that changes every day
// (UTC): HMAC-SHA256(key, date || address) truncated to 16 bytes. Rate
// limits and submissions use it in place of the address, so raw IPs are
// never stored or logged (NDPA: an IP is personal data), and the daily
// change stops one visitor being followed across days.
//
// An IPv4 address is hashed as it is. An IPv6 address is cut to its /64
// first: a subscriber is normally given a whole /64 and can change the
// rest freely, so anything finer would let one phone pose as millions. A
// value that is not an address (or "host:port") is hashed as text.
func HashIP(key []byte, ip string, day time.Time) [16]byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(day.UTC().Format(time.DateOnly)))
	m.Write([]byte{0})
	m.Write(normaliseIP(ip))
	var out [16]byte
	copy(out[:], m.Sum(nil))
	return out
}

// normaliseIP returns the bytes to hash for ip: 4 for IPv4 (including an
// IPv4-mapped IPv6 address), 16 for an IPv6 /64, or a marked string.
func normaliseIP(ip string) []byte {
	ip = strings.TrimSpace(ip)
	a, err := netip.ParseAddr(ip)
	if err != nil {
		ap, perr := netip.ParseAddrPort(ip)
		if perr != nil {
			return append([]byte{'?'}, ip...)
		}
		a = ap.Addr()
	}
	a = a.WithZone("").Unmap()
	if a.Is4() {
		b := a.As4()
		return b[:]
	}
	p, _ := a.Prefix(64) // cannot fail for a 16-byte address
	b := p.Addr().As16()
	return b[:]
}
