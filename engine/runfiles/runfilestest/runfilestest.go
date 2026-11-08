// Package runfilestest is an organisation's storage in memory, for tests
// of run files (engine/runfiles): it keeps voice notes (Ogg) and PDFs and
// refuses anything else, as the real storage refuses what it does not
// accept.
package runfilestest

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/media"
)

// Store is the storage.
type Store struct {
	mu      sync.Mutex
	objects map[string][]byte // tenant/path
	scanned int
	// Threat makes the malware check fail; NoScanner makes it unavailable;
	// FailDelete makes deleting fail (storage down).
	Threat, NoScanner, FailDelete bool
}

// New returns an empty store.
func New() *Store { return &Store{objects: map[string][]byte{}} }

// PutPrivate keeps data under prefix.
func (f *Store) PutPrivate(_ context.Context, tenant uuid.UUID, prefix string, data []byte) (media.File, error) {
	ctype := "audio/ogg"
	switch {
	case bytes.HasPrefix(data, []byte("%PDF")):
		ctype = "application/pdf"
	case !bytes.HasPrefix(data, []byte("OggS")):
		return media.File{}, errors.Join(media.ErrFile, errors.New("not a voice note, PDF or photo"))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := prefix + uuid.NewString()
	f.objects[tenant.String()+"/"+path] = append([]byte(nil), data...)
	return media.File{Path: path, Type: ctype, Size: int64(len(data))}, nil
}

// ScanChatFile is the malware check.
func (f *Store) ScanChatFile(context.Context, []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scanned++
	if f.NoScanner {
		return media.ErrNoScanner
	}
	if f.Threat {
		return media.ErrThreat
	}
	return nil
}

// GetFile reads an object.
func (f *Store) GetFile(_ context.Context, tenant uuid.UUID, path string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[tenant.String()+"/"+path]
	if !ok {
		return nil, "", errors.New("no object")
	}
	return b, "", nil
}

// SignURL is a link to an object.
func (f *Store) SignURL(_ context.Context, tenant uuid.UUID, path string, ttl time.Duration) (string, time.Time, error) {
	return "https://storage.test/" + tenant.String() + "/" + path + "?sig=x", time.Now().Add(ttl), nil
}

// Delete removes objects from the private bucket.
func (f *Store) Delete(_ context.Context, tenant uuid.UUID, bucket string, paths []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailDelete {
		return errors.New("storage down")
	}
	if bucket != media.PrivateBucket {
		return errors.New("wrong bucket")
	}
	for _, p := range paths {
		delete(f.objects, tenant.String()+"/"+p)
	}
	return nil
}

// Count is how many objects are kept.
func (f *Store) Count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.objects) }

// Scanned is how many files were checked for malware.
func (f *Store) Scanned() int { f.mu.Lock(); defer f.mu.Unlock(); return f.scanned }
