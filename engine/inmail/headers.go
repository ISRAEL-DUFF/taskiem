package inmail

import (
	"bytes"
	"crypto/sha256"
	"strings"
)

// A DKIM signature covers the last (lowest) instance of each header it
// names, while a reader takes the first. So a second From put on top of
// a signed message would be read as the sender and never checked. The
// rules here close that gap: headers a message may carry once are refused
// when they come twice, and what is deduplicated is what was signed.

// field is one header field of a raw message: its name in lower case and
// its value with folds joined.
type field struct{ name, value string }

// headerFields reads the header of a raw message, top down, as leniently
// as any verifier would: a line is a new field unless it starts with a
// space or a tab, and the name is what stands before the first colon.
func headerFields(raw []byte) []field {
	var out []field
	for len(raw) > 0 {
		line := raw
		if i := bytes.IndexByte(raw, '\n'); i >= 0 {
			line, raw = raw[:i], raw[i+1:]
		} else {
			raw = nil
		}
		line = bytes.TrimRight(line, "\r")
		if len(line) == 0 {
			break // the body starts here
		}
		if (line[0] == ' ' || line[0] == '\t') && len(out) > 0 {
			out[len(out)-1].value += " " + strings.TrimSpace(string(line))
			continue
		}
		name, value, _ := strings.Cut(string(line), ":")
		out = append(out, field{name: strings.ToLower(strings.TrimSpace(name)), value: strings.TrimSpace(value)})
	}
	return out
}

// once are the headers a message may carry at most once (RFC 5322, 3.6),
// as they are written. The large mailbox providers refuse a message that
// repeats one, so honest mail does not.
var once = map[string]string{
	"from": "From", "sender": "Sender", "reply-to": "Reply-To", "to": "To", "cc": "Cc", "bcc": "Bcc",
	"subject": "Subject", "date": "Date", "message-id": "Message-ID", "in-reply-to": "In-Reply-To", "references": "References",
}

// RepeatedHeader names a header that a message carries more than once
// though it may carry it only once, or "" for a message that keeps the
// rule. Such a message is refused: which instance a signature covers and
// which a person reads would differ.
func RepeatedHeader(raw []byte) string {
	seen := map[string]bool{}
	for _, f := range headerFields(raw) {
		if once[f.name] == "" {
			continue
		}
		if seen[f.name] {
			return once[f.name]
		}
		seen[f.name] = true
	}
	return ""
}

// fromAddress is the one address of a From header, in lower case, or ""
// when the header names none or several: only a single author can be
// proven.
func fromAddress(v string) string {
	list := addresses(v)
	if len(list) != 1 {
		return ""
	}
	return list[0].Address
}

// signedIdentity is what a verified DKIM signature fixes of a message:
// its body hash and the headers it covers, each the instance the
// signature covers (the lowest not yet taken, as DKIM picks them). n is
// the signature's place among the message's DKIM-Signature headers, top
// down, and keys its signed header names. Two deliveries of one signed
// message have the same identity whatever was added around it; nil when
// the signature cannot be found.
func signedIdentity(raw []byte, n int, keys []string) []byte {
	fields := headerFields(raw)
	sig := ""
	for _, f := range fields {
		if f.name != "dkim-signature" {
			continue
		}
		if n == 0 {
			sig = f.value
			break
		}
		n--
	}
	bh := ""
	for _, tag := range strings.Split(sig, ";") {
		if k, v, ok := strings.Cut(tag, "="); ok && strings.TrimSpace(k) == "bh" {
			bh = strings.Join(strings.Fields(v), "")
		}
	}
	if bh == "" {
		return nil
	}
	h := sha256.New()
	h.Write([]byte("bh\x00" + bh + "\x00"))
	taken := map[string]int{} // per name: how many instances, from the bottom, are used
	for _, key := range keys {
		key = strings.ToLower(strings.TrimSpace(key))
		skip := taken[key]
		for i := len(fields) - 1; i >= 0; i-- {
			if fields[i].name != key {
				continue
			}
			if skip > 0 {
				skip--
				continue
			}
			taken[key]++
			h.Write([]byte(key + ":" + strings.Join(strings.Fields(fields[i].value), " ") + "\x00"))
			break
		}
	}
	return h.Sum(nil)
}

// signedFrom is the From address the checks are made for: the one From
// header's one address, read from the raw header as a verifier reads it.
func signedFrom(raw []byte) string {
	fields := headerFields(raw)
	for i := len(fields) - 1; i >= 0; i-- {
		if fields[i].name == "from" {
			return fromAddress(fields[i].value)
		}
	}
	return ""
}
