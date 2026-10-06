package webauthn

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"math/big"
)

type coseKey struct {
	alg    int
	ec     *ecdsa.PublicKey
	ed     ed25519.PublicKey
	rsaKey *rsa.PublicKey
}

func intOf(v any) (int, bool) {
	n, ok := v.(int64)
	return int(n), ok
}

// parseCOSE reads a COSE_Key (RFC 9053) for the supported algorithms.
func parseCOSE(raw []byte) (coseKey, error) {
	v, rest, err := decodeCBOR(raw)
	if err != nil || len(rest) != 0 {
		return coseKey{}, invalid("public key is not CBOR")
	}
	m, ok := v.(map[any]any)
	if !ok {
		return coseKey{}, invalid("public key is not a map")
	}
	kty, _ := intOf(m[int64(1)])
	alg, _ := intOf(m[int64(3)])
	k := coseKey{alg: alg}
	switch {
	case alg == AlgES256 && kty == 2:
		crv, _ := intOf(m[int64(-1)])
		x, _ := m[int64(-2)].([]byte)
		y, _ := m[int64(-3)].([]byte)
		if crv != 1 || len(x) != 32 || len(y) != 32 {
			return coseKey{}, invalid("ES256 key is not on P-256")
		}
		pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...)) // checks the point
		if err != nil {
			return coseKey{}, invalid("ES256 key is not a valid point")
		}
		k.ec = pub
	case alg == AlgEdDSA && kty == 1:
		crv, _ := intOf(m[int64(-1)])
		x, _ := m[int64(-2)].([]byte)
		if crv != 6 || len(x) != ed25519.PublicKeySize {
			return coseKey{}, invalid("EdDSA key is not Ed25519")
		}
		k.ed = ed25519.PublicKey(x)
	case alg == AlgRS256 && kty == 3:
		n, _ := m[int64(-1)].([]byte)
		e, _ := m[int64(-2)].([]byte)
		if len(n)*8 < 2048 || len(e) == 0 || len(e) > 4 {
			return coseKey{}, invalid("RS256 key is too short")
		}
		k.rsaKey = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	default:
		return coseKey{}, invalid("unsupported key (kty %d, alg %d)", kty, alg)
	}
	return k, nil
}

func (k coseKey) verify(data, sig []byte) bool {
	switch {
	case k.ec != nil:
		sum := sha256.Sum256(data)
		return ecdsa.VerifyASN1(k.ec, sum[:], sig)
	case k.ed != nil:
		return ed25519.Verify(k.ed, data, sig)
	case k.rsaKey != nil:
		sum := sha256.Sum256(data)
		return rsa.VerifyPKCS1v15(k.rsaKey, crypto.SHA256, sum[:], sig) == nil
	}
	return false
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}
