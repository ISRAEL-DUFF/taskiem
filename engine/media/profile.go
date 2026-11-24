package media

import "fmt"

// MaxProfilePhotoBytes is the most a person's cleaned photo may be.
const MaxProfilePhotoBytes = 2 << 20

// CheckProfilePhoto checks a person's own photo (their account's, on You:
// docs/spec/taskiem-for-everyone.md §5.1, §7) and cleans it as every
// image is cleaned: a JPEG, PNG or WebP of at most MaxImageBytes and
// MaxPixels, turned upright and re-encoded, which drops its metadata
// (location, camera, owner). It answers the 800-pixel size (or the image's
// own, when smaller) and its type. Being re-encoded from its pixels, it
// carries nothing a malware scan would look for, so it is not scanned.
func CheckProfilePhoto(data []byte) (string, []byte, error) {
	if len(data) == 0 {
		return "", nil, fmt.Errorf("%w: the photo is empty", ErrImage)
	}
	sizes, err := processImage(data)
	if err != nil {
		return "", nil, err
	}
	pick := sizes[len(sizes)-1]
	for _, s := range sizes {
		if s.width <= 800 {
			pick = s
			break
		}
	}
	if len(pick.data) > MaxProfilePhotoBytes {
		return "", nil, fmt.Errorf("%w: larger than %d MB once made smaller", ErrImage, MaxProfilePhotoBytes>>20)
	}
	return pick.ctype, pick.data, nil
}
