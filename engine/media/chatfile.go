package media

import (
	"fmt"
	"net/http"
	"time"

	"github.com/israel-duff/taskiem/engine/voice"
)

// Files in a conversation between a person and a business in the app
// (docs/spec/taskiem-for-everyone.md §8.5): photos up to 10 MB, voice
// notes up to 5 minutes, files up to 25 MB. Each side keeps its own copy,
// so a file is checked and cleaned once here, then stored by each side.
const (
	MaxChatPhotoBytes = MaxImageBytes
	MaxChatVoice      = 5 * time.Minute
	// MaxChatVoiceBytes holds a voice note whose length cannot be read
	// from it (WebM, MP3, M4A: only Ogg's is read) to about five minutes
	// of speech.
	MaxChatVoiceBytes = 5 << 20
	MaxChatFileBytes  = MaxFileBytes
)

// CheckChatFile checks a photo, voice note or PDF sent in an app
// conversation and cleans it as PutChatFile and PutFile do, without
// storing it: a photo turned upright and stripped of its metadata (its
// largest size), a voice note stripped of its tags (Ogg up to five
// minutes, by its own length; the others by size), a PDF refused with
// scripts or embedded files. It answers the file's type and the bytes to
// keep. The caller scans the file for malware first (ScanChatFile).
func CheckChatFile(data []byte) (string, []byte, error) {
	if len(data) == 0 {
		return "", nil, fmt.Errorf("%w: the file is empty", ErrFile)
	}
	ctype, clean, err := cleanVoice(data)
	if err != nil {
		return "", nil, err
	}
	if ctype != "" {
		// Ogg by its own length; an Ogg whose length cannot be read, and
		// the others, by size.
		d, ok := time.Duration(0), false
		if ctype == "audio/ogg" {
			d, ok = voice.OggDuration(clean)
		}
		if (ok && d > MaxChatVoice) || (!ok && len(clean) > MaxChatVoiceBytes) {
			return "", nil, fmt.Errorf("%w: a voice note longer than 5 minutes", ErrFile)
		}
		return ctype, clean, nil
	}
	switch sniffed := http.DetectContentType(data); sniffed {
	case "application/pdf":
		if len(data) > MaxChatFileBytes {
			return "", nil, fmt.Errorf("%w: larger than %d MB", ErrFile, MaxChatFileBytes>>20)
		}
		if err := checkPDF(data); err != nil {
			return "", nil, err
		}
		return sniffed, data, nil
	case "image/jpeg", "image/png", "image/webp":
		if len(data) > MaxChatPhotoBytes {
			return "", nil, fmt.Errorf("%w: a photo larger than %d MB", ErrImage, MaxChatPhotoBytes>>20)
		}
		sizes, err := processImage(data)
		if err != nil {
			return "", nil, err
		}
		return sizes[0].ctype, sizes[0].data, nil
	default:
		return "", nil, fmt.Errorf("%w: a %s file; send a photo, a voice note or a PDF", ErrFile, sniffed)
	}
}
