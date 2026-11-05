package db

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/secure/precis"
)

// scramIterations is Postgres's default (scram_iterations).
const scramIterations = 4096

// ScramVerifier is the SCRAM-SHA-256 verifier Postgres stores for
// password, with a fresh salt: "SCRAM-SHA-256$<iterations>:<salt>$<StoredKey>:<ServerKey>"
// (RFC 5802 and 7677, the form psql's \password sends). A CREATE or ALTER
// ROLE given it instead of the password stores it as it is, so the
// password never appears in a statement, which Postgres logs when the
// statement fails (log_min_error_statement) or under log_statement=ddl.
func ScramVerifier(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return scramVerifier(password, salt)
}

func scramVerifier(password string, salt []byte) (string, error) {
	// SASLprep, as Postgres and pgx apply it; a password it refuses is used
	// as it is, as they do.
	if p, err := precis.OpaqueString.String(password); err == nil {
		password = p
	}
	salted, err := pbkdf2.Key(sha256.New, password, salt, scramIterations, sha256.Size)
	if err != nil {
		return "", err
	}
	mac := func(msg string) []byte {
		h := hmac.New(sha256.New, salted)
		h.Write([]byte(msg))
		return h.Sum(nil)
	}
	stored := sha256.Sum256(mac("Client Key"))
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", scramIterations, b64(salt), b64(stored[:]), b64(mac("Server Key"))), nil
}

// loginStatement is the CREATE or ALTER ROLE (verb) that makes login a
// login, with password's verifier when it has one.
func loginStatement(verb, login, password string) (string, error) {
	stmt := verb + " ROLE " + pgx.Identifier{login}.Sanitize() + " LOGIN NOBYPASSRLS" //nolint:misspell // pgx API
	if password == "" {
		return stmt, nil
	}
	v, err := ScramVerifier(password)
	if err != nil {
		return "", err
	}
	// Base64, digits and $ : only, so no quote to double; doubled anyway.
	return stmt + " PASSWORD '" + strings.ReplaceAll(v, "'", "''") + "'", nil
}
