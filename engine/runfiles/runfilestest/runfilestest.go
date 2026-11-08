// Package runfilestest is an organisation's storage in memory, for tests
// of run files (engine/runfiles): it keeps voice notes (Ogg) and PDFs and
// refuses anything else, as the real storage refuses what it does not
// accept.
package runfilestest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/runfiles"
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

// Files is one run's file store in memory, as a connector sees it
// (connector.Files): what connector tests keep and read.
type Files struct {
	mu       sync.Mutex
	data     map[string][]byte
	refs     map[string]map[string]any
	personal map[string]bool
	// Refuse makes Put refuse every file (malware, too long).
	Refuse bool
}

// NewFiles returns an empty store.
func NewFiles() *Files {
	return &Files{data: map[string][]byte{}, refs: map[string]map[string]any{}, personal: map[string]bool{}}
}

// Put keeps data; Ogg is audio/ogg, MP3 (ID3) audio/mpeg, PDF
// application/pdf.
func (f *Files) Put(_ context.Context, name string, data []byte, personal bool) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Refuse {
		return nil, runfiles.ErrRefused
	}
	typ := "application/octet-stream"
	switch {
	case bytes.HasPrefix(data, []byte("OggS")):
		typ = "audio/ogg"
	case bytes.HasPrefix(data, []byte("ID3")):
		typ = "audio/mpeg"
	case bytes.HasPrefix(data, []byte("%PDF")):
		typ = "application/pdf"
	}
	id := uuid.NewString()
	sum := sha256.Sum256(data)
	ref := map[string]any{"file": id, "name": name, "type": typ, "size": int64(len(data)), "sha256": hex.EncodeToString(sum[:])}
	f.data[id], f.refs[id], f.personal[id] = append([]byte(nil), data...), ref, personal
	return ref, nil
}

// Read answers a file's bytes and reference.
func (f *Files) Read(_ context.Context, ref any) ([]byte, map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := ""
	switch x := ref.(type) {
	case string:
		id = x
	case map[string]any:
		id, _ = x["file"].(string)
	}
	b, ok := f.data[id]
	if !ok {
		return nil, nil, runfiles.ErrNotFound
	}
	return b, f.refs[id], nil
}

// Kept is a kept file's bytes and whether it was a person's.
func (f *Files) Kept(id string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data[id], f.personal[id]
}

// Len is how many files are kept.
func (f *Files) Len() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.data) }
