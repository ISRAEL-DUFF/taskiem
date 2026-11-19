package peoplechats

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/lru"
)

// Each person's copy of a people chat is sealed under a data key of their
// own (people-chatting §6.2, §13): 32 random bytes, kept in person_keys
// sealed with the platform's root key (the KMS) for that person alone, so a
// copy of the database opens nothing. Deleting the person's key with their
// account leaves whatever of their copy a backup still holds unreadable.
// Every value is sealed with AES-GCM, bound to its owner and to the row it
// belongs to, so a sealed value moved to another row or person does not
// open.

// Platform seals small values with the platform's root key (secrets.Vault).
type Platform interface {
	SealPlatform(ctx context.Context, purpose string, value []byte) (sealed, kmsKey string, err error)
	OpenPlatform(ctx context.Context, purpose, kmsKey, sealed string) ([]byte, error)
}

// ErrNoKey: the person has no key (yet, or any more: their account was
// deleted), so nothing of theirs can be sealed or opened.
var ErrNoKey = errors.New("the person has no key")

// Keys hands out people's data keys.
type Keys struct {
	KMS   Platform
	cache *lru.Cache[string, []byte]
}

// NewKeys is Keys over the platform's root key, with keys kept opened in
// memory for a few minutes.
func NewKeys(kms Platform) *Keys {
	return &Keys{KMS: kms, cache: lru.New[string, []byte](4096, 5*time.Minute)}
}

func purpose(person uuid.UUID) string { return "person-key:" + person.String() }

// KeyTx is the person's data key, read in the caller's transaction (their
// own scope, or the scope of a conversation reaching them); made the first
// time when create is set.
func (k *Keys) KeyTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, create bool) ([]byte, error) {
	if k == nil || k.KMS == nil {
		return nil, fmt.Errorf("people chats: no key service")
	}
	for range 2 {
		var sealed, kmsKey string
		err := tx.QueryRow(ctx, `SELECT sealed, kms_key FROM person_keys WHERE person_id = $1`, person).Scan(&sealed, &kmsKey)
		if err == nil {
			ck := person.String() + "\x00" + sealed
			if key, ok := k.cache.Get(ck); ok {
				return key, nil
			}
			key, err := k.KMS.OpenPlatform(ctx, purpose(person), kmsKey, sealed)
			if err != nil {
				return nil, err
			}
			if len(key) != 32 {
				return nil, fmt.Errorf("people chats: a person's key is not 32 bytes")
			}
			k.cache.Put(ck, key)
			return key, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if !create {
			return nil, ErrNoKey
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		sealed, kmsKey, err = k.KMS.SealPlatform(ctx, purpose(person), key)
		if err != nil {
			return nil, err
		}
		// Made at once by two transactions: the one kept is read again.
		if _, err := tx.Exec(ctx, `INSERT INTO person_keys (person_id, sealed, kms_key) VALUES ($1, $2, $3) ON CONFLICT (person_id) DO NOTHING`,
			person, sealed, kmsKey); err != nil {
			return nil, err
		}
	}
	return nil, ErrNoKey
}

// Forget drops what is kept in memory of a person's key (their account was
// deleted).
func (k *Keys) Forget() {
	if k != nil && k.cache != nil {
		k.cache.Clear()
	}
}

func aad(owner uuid.UUID, row string) []byte {
	return []byte("taskiem/people/v1\x00" + owner.String() + "\x00" + row)
}

// Seal seals plain under key for its owner and row.
func Seal(key []byte, owner uuid.UUID, row string, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, aad(owner, row)), nil
}

// Open opens what Seal sealed for the same owner and row.
func Open(key []byte, owner uuid.UUID, row string, sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, fmt.Errorf("people chats: a sealed value is too short")
	}
	return gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], aad(owner, row))
}
