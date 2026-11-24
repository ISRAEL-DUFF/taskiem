package peoplechats

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A group (people-chatting §13, stage 5b) keeps one copy of its messages,
// sealed under a data key of the group's own: 32 random bytes, kept in
// group_keys sealed with the platform's root key for that group alone
// (purpose group-key:{id}), as a person's key is. Values are sealed with
// Seal and Open, bound to the group (as owner) and their row, so a sealed
// value moved to another group or row does not open. Deleting the group
// deletes its key.

func groupPurpose(group uuid.UUID) string { return "group-key:" + group.String() }

// NewGroupKey makes a group's data key and seals it with the platform's
// root key: the key, and what group_keys keeps.
func (k *Keys) NewGroupKey(ctx context.Context, group uuid.UUID) (key []byte, sealed, kmsKey string, err error) {
	if k == nil || k.KMS == nil {
		return nil, "", "", fmt.Errorf("people chats: no key service")
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, "", "", err
	}
	sealed, kmsKey, err = k.KMS.SealPlatform(ctx, groupPurpose(group), key)
	if err != nil {
		return nil, "", "", err
	}
	k.cache.Put("g\x00"+group.String()+"\x00"+sealed, key)
	return key, sealed, kmsKey, nil
}

// OpenGroupKey opens a group's sealed key (as group_keys keeps it).
func (k *Keys) OpenGroupKey(ctx context.Context, group uuid.UUID, sealed, kmsKey string) ([]byte, error) {
	if k == nil || k.KMS == nil {
		return nil, fmt.Errorf("people chats: no key service")
	}
	ck := "g\x00" + group.String() + "\x00" + sealed
	if key, ok := k.cache.Get(ck); ok {
		return key, nil
	}
	key, err := k.KMS.OpenPlatform(ctx, groupPurpose(group), kmsKey, sealed)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("people chats: a group's key is not 32 bytes")
	}
	k.cache.Put(ck, key)
	return key, nil
}

// GroupKeyTx is the group's data key, read in the caller's transaction (a
// member's, or someone invited: group_keys' policy); ErrNoKey when it is
// not to be read.
func (k *Keys) GroupKeyTx(ctx context.Context, tx pgx.Tx, group uuid.UUID) ([]byte, error) {
	var sealed, kmsKey string
	err := tx.QueryRow(ctx, `SELECT sealed, kms_key FROM group_keys WHERE group_id = $1`, group).Scan(&sealed, &kmsKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoKey
	}
	if err != nil {
		return nil, err
	}
	return k.OpenGroupKey(ctx, group, sealed, kmsKey)
}

// GroupSearchKey is the key of a group's search index, derived from the
// group's own key: its tokens are alike only within the group, never
// with another group's or a person's own index.
func GroupSearchKey(key []byte, group uuid.UUID) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("taskiem-group-search:v1:" + group.String()))
	return m.Sum(nil)
}
