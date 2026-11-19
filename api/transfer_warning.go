package api

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/people"
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
//     bank, and the others in people.AsksForTransfer).
//
// It cannot tell a personal account from a business's, so it warns on
// both; the warning says to pay only through the pay link. Cards (an
// order, a pay link) never warn: they are made from the business's own
// records. The rule itself is people.AsksForTransfer, which the checks
// on strangers' messages in people chats reuse.

func warnTransfer(text string) bool { return people.AsksForTransfer(text) }

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
