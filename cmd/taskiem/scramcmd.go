package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/israel-duff/taskiem/engine/db"
)

// scramVerifierCmd reads a password (one line) on stdin and prints its
// SCRAM-SHA-256 verifier, for a CREATE or ALTER ROLE that must not carry
// the password itself: Postgres logs a failing statement's text. The VM
// kit (taskiem-vps setup) uses it for the logins it makes.
func scramVerifierCmd(stdin io.Reader, stdout io.Writer) error {
	line, err := bufio.NewReader(io.LimitReader(stdin, 4096)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	pw := strings.TrimRight(line, "\r\n")
	if pw == "" {
		return errors.New("scram-verifier: no password on stdin")
	}
	v, err := db.ScramVerifier(pw)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, v)
	return err
}
