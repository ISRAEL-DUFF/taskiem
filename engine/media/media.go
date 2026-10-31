// Package media stores a tenant's files in its own Xenibase project's
// storage (Phase 5, S1; docs/media.md): storefront photos, which anyone
// may see, and files people send in hosted forms, which only the tenant
// may.
//
// Every tenant's project gets two buckets. PublicBucket holds images that
// were checked, turned upright, stripped of their metadata and stored at
// each of Widths, under paths named by their content, so they are cached
// for good and served by the project's host (or a CDN) without Taskiem.
// PrivateBucket holds form uploads, reached only through short-lived
// signed URLs. Taskiem reaches both with the project's API token through
// Xenibase's management file API, so no storage policy is needed and none
// can open them.
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/lru"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// The buckets every tenant's project has.
const (
	PublicBucket  = "taskiem-public"
	PrivateBucket = "taskiem-private"
)

// MaxFileBytes caps a private file (a payslip, a scanned ID).
const MaxFileBytes = 25 << 20

// MaxSignedTTL is the longest a signed URL may last (Xenibase's limit).
const MaxSignedTTL = 7 * 24 * time.Hour

var (
	// ErrFile wraps every refusal of a private file.
	ErrFile = errors.New("media: not an accepted file")
	// ErrBucket says a bucket's settings are wrong and could not be put
	// right: a private bucket that is public is never written to.
	ErrBucket = errors.New("media: the storage bucket is misconfigured")
)

var wantBuckets = []bucketInfo{
	{ID: PublicBucket, Public: true, FileSizeLimit: ptr(int64(MaxImageBytes)), AllowedMIMETypes: []string{"image/jpeg", "image/png"},
		CacheSeconds: ptr(int64(365 * 24 * 3600))},
	{ID: PrivateBucket, Public: false, FileSizeLimit: ptr(int64(MaxFileBytes)), AllowedMIMETypes: []string{"image/jpeg", "image/png", "application/pdf",
		"audio/ogg", "audio/webm", "audio/mpeg", "audio/mp4"},
		CacheSeconds: ptr(int64(0))},
}

// Service stores tenants' files.
type Service struct {
	// Credentials returns a tenant's Xenibase project connection: token,
	// project_id and base_url for the management API, api_url for its own
	// host (datatables.Service.Credentials).
	Credentials func(ctx context.Context, tenant uuid.UUID) (map[string]string, error)
	Guard       *egress.Guard
	Logger      *slog.Logger

	ready *lru.Cache[uuid.UUID, bool]
}

// Image is a stored image: its size and the URL of each width.
type Image struct {
	Hash          string
	Width, Height int
	Type          string
	// URLs are the public URLs by width, widest first.
	URLs map[int]string
	// Paths are the objects stored, for deleting them.
	Paths []string
}

// File is a stored private file.
type File struct {
	Path string
	Type string
	Size int64
}

// PutImage checks, cleans and stores an image in the tenant's public
// bucket. Storing the same image again stores nothing new.
func (s *Service) PutImage(ctx context.Context, tenant uuid.UUID, data []byte) (Image, error) {
	sizes, err := processImage(data)
	if err != nil {
		return Image{}, err
	}
	c, creds, err := s.client(ctx, tenant)
	if err != nil {
		return Image{}, err
	}
	sum := sha256.Sum256(data)
	img := Image{Hash: hex.EncodeToString(sum[:16]), Width: sizes[0].width, Height: sizes[0].height, Type: sizes[0].ctype, URLs: map[int]string{}}
	for _, r := range sizes {
		ext := ".jpg"
		if r.ctype == "image/png" {
			ext = ".png"
		}
		path := "images/" + img.Hash + "/" + strconv.Itoa(r.width) + ext
		if _, err := c.put(ctx, PublicBucket, path, r.ctype, r.data); err != nil {
			return Image{}, err
		}
		img.Paths = append(img.Paths, path)
		img.URLs[r.width] = PublicURL(creds["api_url"], path)
	}
	return img, nil
}

// PutFile checks and stores a file someone sent in a form, in the
// tenant's private bucket. Images are cleaned as PutImage cleans them and
// kept at full size; PDFs are refused if they carry scripts or actions.
func (s *Service) PutFile(ctx context.Context, tenant uuid.UUID, data []byte) (File, error) {
	if len(data) > MaxFileBytes {
		return File{}, fmt.Errorf("%w: larger than %d MB", ErrFile, MaxFileBytes>>20)
	}
	ctype := http.DetectContentType(data)
	ext := ".pdf"
	switch ctype {
	case "application/pdf":
		if err := checkPDF(data); err != nil {
			return File{}, err
		}
	case "image/jpeg", "image/png", "image/webp":
		sizes, err := processImage(data)
		if err != nil {
			return File{}, err
		}
		data, ctype, ext = sizes[0].data, sizes[0].ctype, ".jpg"
		if ctype == "image/png" {
			ext = ".png"
		}
	default:
		return File{}, fmt.Errorf("%w: a %s file; send a PDF or a photo", ErrFile, ctype)
	}
	c, _, err := s.client(ctx, tenant)
	if err != nil {
		return File{}, err
	}
	path := "files/" + uuid.Must(uuid.NewV7()).String() + ext
	o, err := c.put(ctx, PrivateBucket, path, ctype, data)
	if err != nil {
		return File{}, err
	}
	size := o.Size
	if size == 0 {
		size = int64(len(data))
	}
	return File{Path: path, Type: ctype, Size: size}, nil
}

// MaxVoiceBytes caps a voice note (several minutes of speech).
const MaxVoiceBytes = 16 << 20

// voiceType is an audio file's type, by its header: Ogg (WhatsApp's and
// Telegram's voice notes, Firefox's recordings), WebM (Chrome's), MP3, or
// MP4/M4A (Safari's); "" for anything else.
func voiceType(data []byte) string {
	switch {
	case len(data) >= 4 && string(data[:4]) == "OggS":
		return "audio/ogg"
	case len(data) >= 4 && data[0] == 0x1A && data[1] == 0x45 && data[2] == 0xDF && data[3] == 0xA3:
		return "audio/webm"
	case len(data) >= 3 && string(data[:3]) == "ID3", len(data) >= 2 && data[0] == 0xFF && data[1]&0xE0 == 0xE0:
		return "audio/mpeg"
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		return "audio/mp4"
	}
	return ""
}

// AudioType is an audio file's type by its header (Ogg, WebM, MP3 or
// MP4), "" for anything else.
func AudioType(data []byte) string { return voiceType(data) }

// ChatKind is what a file sent in a conversation is: a photo, a PDF, or a
// voice note.
func ChatKind(ctype string) string {
	switch {
	case strings.HasPrefix(ctype, "image/"):
		return "photo"
	case strings.HasPrefix(ctype, "audio/"):
		return "voice"
	}
	return "file"
}

// PutChatFile checks and stores a file sent in a conversation between
// businesses (business links L3) in the tenant's private bucket: a photo
// (cleaned as PutImage cleans it), a PDF (refused with scripts or
// actions), or a voice note (Ogg, WebM, MP3 or MP4 audio, by its header).
// Each business keeps its own copy, in its own storage.
func (s *Service) PutChatFile(ctx context.Context, tenant uuid.UUID, data []byte) (File, error) {
	ctype := voiceType(data)
	if ctype == "" {
		return s.PutFile(ctx, tenant, data)
	}
	if len(data) > MaxVoiceBytes {
		return File{}, fmt.Errorf("%w: a voice note larger than %d MB", ErrFile, MaxVoiceBytes>>20)
	}
	c, _, err := s.client(ctx, tenant)
	if err != nil {
		return File{}, err
	}
	ext := map[string]string{"audio/ogg": ".ogg", "audio/webm": ".webm", "audio/mpeg": ".mp3", "audio/mp4": ".m4a"}[ctype]
	path := "chat/" + uuid.Must(uuid.NewV7()).String() + ext
	o, err := c.put(ctx, PrivateBucket, path, ctype, data)
	if err != nil {
		return File{}, err
	}
	size := o.Size
	if size == 0 {
		size = int64(len(data))
	}
	return File{Path: path, Type: ctype, Size: size}, nil
}

// GetFile downloads one of the tenant's private files, for the web app to
// show through the API (its Content-Security-Policy allows images and
// sound from its own origin only): through a signed URL a minute long,
// from the tenant's own project host, through the egress guard.
func (s *Service) GetFile(ctx context.Context, tenant uuid.UUID, path string) ([]byte, string, error) {
	c, creds, err := s.client(ctx, tenant)
	if err != nil {
		return nil, "", err
	}
	signed, _, err := c.sign(ctx, PrivateBucket, path, time.Minute)
	if err != nil {
		return nil, "", err
	}
	u, err := url.Parse(signed)
	if err != nil {
		return nil, "", err
	}
	if !u.IsAbs() {
		base, _ := url.Parse(strings.TrimRight(creds["api_url"], "/"))
		u = base.ResolveReference(u)
	}
	hosts := []string{}
	for _, k := range []string{"api_url", "base_url"} {
		if h, err := url.Parse(creds[k]); err == nil && h.Hostname() != "" {
			hosts = append(hosts, h.Hostname())
		}
	}
	if !slices.Contains(hosts, u.Hostname()) || u.User != nil {
		return nil, "", ErrNotOurs
	}
	hc := s.guard().Client(egress.Policy{Tenant: tenant.String(), Hosts: []string{u.Hostname()}, Purpose: "xenibase:storage"}, 60*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := hc.Do(req) //nolint:gosec // the tenant's own project host, checked above, through the egress guard
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("media: the file answered %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxFileBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > MaxFileBytes {
		return nil, "", ErrNotOurs
	}
	ctype := voiceType(data)
	if ctype == "" {
		ctype = http.DetectContentType(data)
	}
	return data, ctype, nil
}

// SignURL returns a URL that downloads a private file until it expires.
func (s *Service) SignURL(ctx context.Context, tenant uuid.UUID, path string, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 || ttl > MaxSignedTTL {
		return "", time.Time{}, fmt.Errorf("media: a signed URL lasts up to %s", MaxSignedTTL)
	}
	c, _, err := s.client(ctx, tenant)
	if err != nil {
		return "", time.Time{}, err
	}
	return c.sign(ctx, PrivateBucket, path, ttl)
}

// Delete removes objects from one of the tenant's buckets.
func (s *Service) Delete(ctx context.Context, tenant uuid.UUID, bucket string, paths []string) error {
	if bucket != PublicBucket && bucket != PrivateBucket {
		return fmt.Errorf("media: no bucket %q", bucket)
	}
	if len(paths) == 0 {
		return nil
	}
	c, _, err := s.client(ctx, tenant)
	if err != nil {
		return err
	}
	_, err = c.remove(ctx, bucket, paths)
	return err
}

// PublicURL is where a public bucket's object is served on the project's
// own host.
func PublicURL(apiURL, path string) string {
	segs := strings.Split(path, "/")
	for i, p := range segs {
		segs[i] = url.PathEscape(p)
	}
	return strings.TrimRight(apiURL, "/") + "/storage/v1/public/" + PublicBucket + "/" + strings.Join(segs, "/")
}

// client is a storage client for the tenant's project, with its buckets
// checked (once an hour per tenant and process).
func (s *Service) client(ctx context.Context, tenant uuid.UUID) (*storage, map[string]string, error) {
	creds, err := s.Credentials(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "media.storage"}), tenant)
	if err != nil {
		return nil, nil, err
	}
	base, token, project := strings.TrimRight(creds["base_url"], "/"), creds["token"], creds["project_id"]
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || token == "" || project == "" || creds["api_url"] == "" {
		return nil, nil, errors.New("media: the tenant's Xenibase connection has no token, project or URLs")
	}
	c := &storage{base: base, project: project, token: token,
		hc: s.guard().Client(egress.Policy{Tenant: tenant.String(), Hosts: []string{u.Hostname()}, Purpose: "xenibase:storage"}, 60*time.Second)}
	if s.ready == nil {
		s.ready = lru.New[uuid.UUID, bool](10000, time.Hour)
	}
	if _, ok := s.ready.Get(tenant); !ok {
		if err := s.ensureBuckets(ctx, c); err != nil {
			return nil, nil, err
		}
		s.ready.Put(tenant, true)
	}
	return c, creds, nil
}

// ensureBuckets makes the two buckets, or puts their settings right. A
// bucket someone made public that should be private is made private
// again before anything is written to it.
func (s *Service) ensureBuckets(ctx context.Context, c *storage) error {
	have, err := c.buckets(ctx)
	if err != nil {
		return err
	}
	for _, want := range wantBuckets {
		i := slices.IndexFunc(have, func(b bucketInfo) bool { return b.ID == want.ID })
		switch {
		case i < 0:
			if err := c.createBucket(ctx, want); err != nil {
				var e *Error
				if !errors.As(err, &e) || e.Status != http.StatusConflict {
					return err
				}
			}
		case !sameBucket(have[i], want):
			if have[i].Public != want.Public {
				s.logger().Warn("media: a bucket's visibility was changed outside Taskiem; putting it back", "bucket", want.ID, "public", have[i].Public)
			}
			if err := c.updateBucket(ctx, want); err != nil {
				if !want.Public && have[i].Public {
					return fmt.Errorf("%w: %s is public and could not be made private: %w", ErrBucket, want.ID, err)
				}
				return err
			}
		}
	}
	return nil
}

func sameBucket(a, b bucketInfo) bool {
	eq := func(x, y *int64) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	ta, tb := slices.Clone(a.AllowedMIMETypes), slices.Clone(b.AllowedMIMETypes)
	slices.Sort(ta)
	slices.Sort(tb)
	return a.Public == b.Public && eq(a.FileSizeLimit, b.FileSizeLimit) && slices.Equal(ta, tb) && eq(a.CacheSeconds, b.CacheSeconds)
}

// checkPDF refuses a PDF that can run code or act when opened. Form files
// are also scanned for malware before a run sees them (forms 6.5).
func checkPDF(data []byte) error {
	if !strings.HasPrefix(string(data[:min(len(data), 8)]), "%PDF-") {
		return fmt.Errorf("%w: not a PDF", ErrFile)
	}
	for _, k := range []string{"/JavaScript", "/JS", "/Launch", "/EmbeddedFile", "/OpenAction", "/AA", "/RichMedia", "/XFA"} {
		if hasPDFName(data, k) {
			return fmt.Errorf("%w: the PDF carries %s, which is not accepted", ErrFile, k)
		}
	}
	return nil
}

// hasPDFName reports whether data holds the name k, not as the start of a
// longer name (/JS but not /JSON).
func hasPDFName(data []byte, k string) bool {
	s := string(data)
	for i := strings.Index(s, k); i >= 0; {
		end := i + len(k)
		if end >= len(s) || !isNameChar(s[end]) {
			return true
		}
		j := strings.Index(s[end:], k)
		if j < 0 {
			return false
		}
		i = end + j
	}
	return false
}

func isNameChar(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-' || b == '.'
}

func (s *Service) guard() *egress.Guard {
	if s.Guard != nil {
		return s.Guard
	}
	return &egress.Guard{Logger: s.logger()}
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func ptr[T any](v T) *T { return &v }

// ErrNotOurs refuses a URL that is not one of the tenant's public images.
var ErrNotOurs = errors.New("media: not one of the organisation's images")

// FetchPublic downloads one of the tenant's public images, for the web
// app: its own Content-Security-Policy allows images from its origin
// only, so the editor shows photos through the API instead of loading
// them from the project's host. Only URLs under the tenant's own project
// host and its public images path are fetched, through the egress guard.
func (s *Service) FetchPublic(ctx context.Context, tenant uuid.UUID, raw string) ([]byte, string, error) {
	creds, err := s.Credentials(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "media.preview"}), tenant)
	if err != nil {
		return nil, "", err
	}
	api, err := url.Parse(strings.TrimRight(creds["api_url"], "/"))
	if err != nil || api.Host == "" {
		return nil, "", errors.New("media: the tenant's Xenibase connection has no project URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != api.Scheme || u.Host != api.Host || u.User != nil || u.RawQuery != "" ||
		!strings.HasPrefix(u.EscapedPath(), "/storage/v1/public/"+PublicBucket+"/images/") || strings.Contains(u.Path, "..") {
		return nil, "", ErrNotOurs
	}
	c := s.guard().Client(egress.Policy{Tenant: tenant.String(), Hosts: []string{u.Hostname()}, Purpose: "xenibase:storage"}, 30*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.Do(req) //nolint:gosec // the tenant's own project host, checked above, through the egress guard
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("media: the image answered %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	ctype := http.DetectContentType(data)
	if len(data) > MaxImageBytes || (ctype != "image/jpeg" && ctype != "image/png") {
		return nil, "", ErrNotOurs
	}
	return data, ctype, nil
}
