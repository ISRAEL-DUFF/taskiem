package secrets

import (
	"context"
	"fmt"
	"strings"
)

// Sealing what belongs to no tenant yet: a Slack install made from
// Taskiem's website before any business exists (docs/spec/chat-way-in.md
// 9). It is sealed with the platform's KMS root key, as a customer key's
// credentials are, bound to what it is for, so a value sealed for one use
// cannot be opened as another's.

// SealPlatform seals value for purpose with the platform's root key and
// returns it with the key's name, both to keep.
func (v *Vault) SealPlatform(ctx context.Context, purpose string, value []byte) (sealed, kmsKey string, err error) {
	if v.KMS == nil || v.RootKey == "" {
		return "", "", fmt.Errorf("platform KMS: not configured")
	}
	ct, err := v.KMS.Encrypt(ctx, v.RootKey, append([]byte(platformContext(purpose)), value...))
	if err != nil {
		return "", "", fmt.Errorf("platform KMS: %w", err)
	}
	return ct, v.RootKey, nil
}

// OpenPlatform opens what SealPlatform sealed for the same purpose.
func (v *Vault) OpenPlatform(ctx context.Context, purpose, kmsKey, sealed string) ([]byte, error) {
	if v.KMS == nil {
		return nil, fmt.Errorf("platform KMS: not configured")
	}
	pt, err := v.KMS.Decrypt(ctx, kmsKey, sealed)
	if err != nil {
		return nil, fmt.Errorf("platform KMS: %w", err)
	}
	raw, ok := strings.CutPrefix(string(pt), platformContext(purpose))
	if !ok {
		return nil, fmt.Errorf("platform KMS: sealed for another use")
	}
	return []byte(raw), nil
}

func platformContext(purpose string) string {
	return "taskiem/platform-sealed/v1\x00" + purpose + "\x00"
}
