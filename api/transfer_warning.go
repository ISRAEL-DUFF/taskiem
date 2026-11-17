package api

import (
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Paying a business is only ever through Taskiem's pay links (taskiem-for-
// everyone §15, fraud): the app warns on a business's message that asks
// for a transfer to an account. warnTransfer is that check, kept simple
// and conservative, so that it rarely warns on an ordinary message: a
// message warns when it holds
//   - an account number: ten digits standing alone (a Nigerian NUBAN),
//     written together or in groups ("0123456789", "0123 456 789",
//     "012-345-6789"), never part of a longer number such as a phone
//     ("08031234567"), and
//   - a word of banking: account, acct, a/c, bank, or a bank's name
//     (opay, palmpay, moniepoint, kuda, gtbank, access, zenith, uba, first
//     bank, and the others listed in bankWords).
//
// It cannot tell a personal account from a business's, so it warns on
// both; the warning says to pay only through the pay link. Cards (an
// order, a pay link) never warn: they are made from the business's own
// records.

// nubanRe finds ten digits standing alone, in groups or together.
var nubanRe = regexp.MustCompile(`(?:^|[^0-9])([0-9](?:[ \-.]?[0-9]){9})(?:$|[^0-9])`)

// bankWords are the words that make a ten-digit number an account number.
var bankWords = regexp.MustCompile(`(?i)\b(account|acct|acc no|a/c|bank|opay|palmpay|palm pay|moniepoint|kuda|gtb|gtbank|guaranty|access|zenith|uba|first ?bank|fidelity|union bank|sterling|wema|alat|polaris|fcmb|stanbic|ecobank|keystone|providus|jaiz|globus|titan|unity bank|heritage|carbon|vfd|paga)\b`)

func warnTransfer(text string) bool {
	if len(text) < 10 {
		return false
	}
	for _, m := range nubanRe.FindAllStringSubmatch(text, -1) {
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, m[1])
		if len(digits) == 10 {
			return bankWords.MatchString(text)
		}
	}
	return false
}

// typingErr reads the refusals of taskiem_customer_typing: the business
// blocked the person (blocked), or the person blocked the business
// (stopped), said as stopped.
func typingErr(err error, stopped error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Hint {
		case "blocked":
			return errDoorBlocked
		case "stopped":
			return stopped
		}
	}
	return err
}
