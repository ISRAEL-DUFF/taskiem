// Package peoplesafety holds what keeping people chats safe needs beside
// the API (docs/spec/people-chatting.md §8, "As built: stage 5a gaps,
// safety and moderation"; decision 0069 points 3, 5 and 11): the sealed
// boxes reports and held messages are kept in for moderators, the request
// allowance with its abuse signals, the moderators' actions, retention,
// and the half-years lawful requests are counted by.
//
// Reports and held messages are sealed under a data key of their own (32
// random bytes, kept sealed with the platform's root key for that report
// or message alone), so a copy of the database opens none of them.
// People chats are kept by Taskiem and readable by its servers; nothing
// here makes them end-to-end encrypted, and nothing may say so.
package peoplesafety

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/peoplechats"
)

// Box is a report's or a held message's own data key.
type Box struct {
	key []byte
	// Sealed and KMSKey are the key as kept: sealed with the platform's
	// root key for its purpose alone.
	Sealed, KMSKey string
	owner          uuid.UUID
}

// ReportPurpose names a report's key for the platform's root key: a key
// sealed for one report opens for no other.
func ReportPurpose(id uuid.UUID) string { return "people-report:" + id.String() }

// HeldPurpose is a held message's.
func HeldPurpose(id uuid.UUID) string { return "people-held:" + id.String() }

// ErrNoKMS: the server has no platform key service, so nothing is sealed.
var ErrNoKMS = errors.New("people safety: no key service")

// NewBox makes a new key for owner (the report's or held message's id),
// sealed for purpose.
func NewBox(ctx context.Context, kms peoplechats.Platform, purpose string, owner uuid.UUID) (Box, error) {
	if kms == nil {
		return Box{}, ErrNoKMS
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return Box{}, err
	}
	sealed, kmsKey, err := kms.SealPlatform(ctx, purpose, key)
	if err != nil {
		return Box{}, err
	}
	return Box{key: key, Sealed: sealed, KMSKey: kmsKey, owner: owner}, nil
}

// OpenBox opens a kept key.
func OpenBox(ctx context.Context, kms peoplechats.Platform, purpose string, owner uuid.UUID, sealed, kmsKey string) (Box, error) {
	if kms == nil {
		return Box{}, ErrNoKMS
	}
	key, err := kms.OpenPlatform(ctx, purpose, kmsKey, sealed)
	if err != nil {
		return Box{}, err
	}
	if len(key) != 32 {
		return Box{}, fmt.Errorf("people safety: a box key is not 32 bytes")
	}
	return Box{key: key, Sealed: sealed, KMSKey: kmsKey, owner: owner}, nil
}

// Seal seals plain for one row of the box (MessagesRow, or FileRow).
func (b Box) Seal(row string, plain []byte) ([]byte, error) {
	return peoplechats.Seal(b.key, b.owner, row, plain)
}

// Open opens what Seal sealed for the same row.
func (b Box) Open(row string, sealed []byte) ([]byte, error) {
	return peoplechats.Open(b.key, b.owner, row, sealed)
}

// FileRow is a file's row in a box.
func FileRow(message uuid.UUID) string { return "f:" + message.String() }

// MessagesRow is the row of a report's messages, or a held message's.
const MessagesRow = "m"

// Retention (people-chatting §14, the moderation policy).
const (
	// ReportsKept: a reviewed report, with its messages and files, is
	// kept a year after it was reviewed; an open one until it is.
	ReportsKept = 365 * 24 * time.Hour
	// HeldKept: a held message is kept 90 days after it was held, or
	// after a moderator decided it.
	HeldKept = 90 * 24 * time.Hour
	// AppealWindow: a closure waits this long for an appeal before the
	// account is closed (deleted as the person would delete it).
	AppealWindow = 30 * 24 * time.Hour
	// ReportFilesMax: at most this many bytes of photos and files are
	// copied into one report (the newest first); what is left out is said.
	ReportFilesMax = 50 << 20
)

// Moderators' actions.
const (
	ActionWarn    = "warn"
	ActionLimit   = "limit"
	ActionSuspend = "suspend"
	ActionClose   = "close"
)

// Signals are what a sender's request allowance weighs (§8.2, §8.4).
type Signals struct {
	// Age of the account.
	Age time.Duration
	// Accepted and Refused (deleted or blocked) requests they sent.
	Accepted, Refused int
	// ReportedBy: how many people reported them in the last 30 days.
	ReportedBy int
	// DeviceAccounts: how many accounts used the devices they signed in
	// on (they included).
	DeviceAccounts int
	// Limited: a moderator limited them (people_moderation, in force).
	Limited bool
}

// FirstWeek is the account's first week.
func (s Signals) FirstWeek() bool { return s.Age < 7*24*time.Hour }

// SharedDevice is a first-week account on a device several accounts used.
func (s Signals) SharedDevice() bool {
	return s.FirstWeek() && s.DeviceAccounts >= SharedDeviceAccounts
}

// SharedDeviceAccounts is how many accounts on one device make it shared.
const SharedDeviceAccounts = 3

// Allowance is how many requests a sender may have waiting at once and
// send in a day. It grows with the account's age (a week, 30 days, 90
// days) and with requests people accepted; it shrinks when most of what
// they sent was deleted or blocked, when several people reported them,
// when their device has several accounts in their first week, and to the
// least when a moderator limited them.
func Allowance(s Signals) (waiting, perDay int) {
	switch {
	case s.FirstWeek():
		waiting, perDay = 2, 3
	case s.Age < 30*24*time.Hour:
		waiting, perDay = 5, 10
	case s.Age < 90*24*time.Hour:
		waiting, perDay = 8, 15
	default:
		waiting, perDay = 10, 20
	}
	waiting += min(s.Accepted/2, 15)
	perDay += min(s.Accepted, 40)
	if s.Refused >= 3 && s.Refused*2 > s.Accepted+s.Refused {
		waiting, perDay = max(1, waiting/3), max(1, perDay/3)
	}
	switch {
	case s.ReportedBy >= 5:
		waiting, perDay = 1, 1
	case s.ReportedBy >= 2:
		waiting, perDay = max(1, waiting/3), max(1, perDay/3)
	}
	if s.SharedDevice() {
		waiting, perDay = 1, 1
	}
	if s.Limited {
		waiting, perDay = 1, min(perDay, 2)
	}
	return waiting, perDay
}

// Slowed says whether the sender's other limits (new chats an hour and a
// day, messages a minute) are those of an account's first week: in their
// first week, reported by several people, or limited.
func Slowed(s Signals) bool {
	return s.FirstWeek() || s.ReportedBy >= 2 || s.Limited
}

// Half is a half-year, as lawful requests are counted and published.
type Half struct {
	Year int `json:"year"`
	Half int `json:"half"` // 1: January to June; 2: July to December
}

// Label is "January to June 2026".
func (h Half) Label() string {
	if h.Half == 1 {
		return fmt.Sprintf("January to June %d", h.Year)
	}
	return fmt.Sprintf("July to December %d", h.Year)
}
