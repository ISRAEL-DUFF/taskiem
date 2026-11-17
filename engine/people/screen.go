package people

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// The automated checks on strangers' messages (§8.4, §17 Q15): a first
// message, or any message in a chat the other person has not accepted, is
// read for known scam patterns. Only the verdict is kept (warn or hold,
// with the reasons' names; never the words, and nothing for ok).

// Verdicts.
const (
	// VerdictOK: deliver it as it is.
	VerdictOK = "ok"
	// VerdictWarn: deliver it with a warning ("Never send money to someone
	// you have not met").
	VerdictWarn = "warn"
	// VerdictHold: do not deliver it yet; it waits for moderators (or is
	// dropped), and the sender is slowed.
	VerdictHold = "hold"
)

// Reasons a message warns or is held.
const (
	ReasonTransfer   = "transfer"   // an account number with a word of banking (the business chats' transfer warning)
	ReasonSendMoney  = "send_money" // asks for money to be sent ("send money to this account", "pay into my account")
	ReasonPrize      = "prize"      // a prize or win ("you have won", "claim your reward")
	ReasonFee        = "fee"        // a fee to release something ("processing fee", "activation fee")
	ReasonCode       = "code"       // asks for a code, PIN, BVN or card ("send me the code you received")
	ReasonLink       = "link"       // a link, in a message that also warns
	ReasonRepeat     = "repeat"     // the sender's strangers' messages warned many times today
	ReasonInvestment = "investment" // doubling money, guaranteed returns
)

// RepeatHold is how many warned or held stranger messages a sender may
// have in a day before the next one that warns is held.
const RepeatHold = 3

// nubanRe finds ten digits standing alone, in groups or together.
var nubanRe = regexp.MustCompile(`(?:^|[^0-9])([0-9](?:[ \-.]?[0-9]){9})(?:$|[^0-9])`)

// bankWords are the words that make a ten-digit number an account number.
var bankWords = regexp.MustCompile(`(?i)\b(account|acct|acc no|a/c|bank|opay|palmpay|palm pay|moniepoint|kuda|gtb|gtbank|guaranty|access|zenith|uba|first ?bank|fidelity|union bank|sterling|wema|alat|polaris|fcmb|stanbic|ecobank|keystone|providus|jaiz|globus|titan|unity bank|heritage|carbon|vfd|paga)\b`)

// AsksForTransfer is the transfer warning's rule (taskiem-for-everyone
// §15, the business chats' warning; api's warnTransfer): a message that
// holds an account number, ten digits standing alone ("0123456789",
// "0123 456 789", "012-345-6789"), never part of a longer number such as a
// phone, and a word of banking (account, acct, a/c, bank, or a bank's
// name). It cannot tell a personal account from a business's, so it warns
// on both; it is kept simple and conservative so that it rarely warns on
// an ordinary message.
func AsksForTransfer(text string) bool {
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

var (
	sendMoneyRe  = regexp.MustCompile(`(?i)\b(send|transfer|pay|deposit|credit)\b[^.!?\n]{0,40}\b(money|cash|funds?|naira|₦|n\d|amount)\b|\b(send|transfer|pay|deposit)\b[^.!?\n]{0,20}\b(in ?to|to) (this|my|the following|below) (account|acct|number|a/c)\b|\bpay (in ?to|to) my (account|acct|a/c)\b`)
	prizeRe      = regexp.MustCompile(`(?i)\b(you('ve| have)? (just )?won|you are (the|a|our) (lucky )?winner|congratulations?[^.!?\n]{0,40}\b(won|winner|selected|prize|reward)|claim (your|the) (prize|reward|winnings|gift|money)|lucky (draw|winner)|lottery|jackpot|grant (has been )?approved)\b`)
	feeRe        = regexp.MustCompile(`(?i)\b(processing|activation|delivery|clearance|registration|release|transfer|unlock|verification) fee\b`)
	codeRe       = regexp.MustCompile(`(?i)\b(send|give|tell|share|forward) (me )?(the |your |that )?(otp|one[- ]time (pass)?code|code (you|that) (got|received|was sent)|6[- ]digit code|verification code|pin|bvn|nin|atm card|card (number|details)|cvv|password)\b|\b(what is|what's) your (otp|pin|bvn|cvv|password)\b`)
	investmentRe = regexp.MustCompile(`(?i)\b(double your (money|cash|investment)|guaranteed (returns?|profit|income)|\d+ ?% (profit|returns?|interest) (daily|weekly|in \d+ (days?|hours?)))\b`)
	linkRe       = regexp.MustCompile(`(?i)\b(https?://|www\.)\S+|\b[a-z0-9-]+\.(com|ng|net|org|xyz|top|link|info|site|online|click|ly)(/\S*)?\b|\b[a-z0-9-]+\.[a-z]{2,6}/\S+`)
)

// Verdict is what the checks decided of one message.
type Verdict struct {
	Verdict string   `json:"verdict"`
	Reasons []string `json:"reasons,omitempty"`
}

// Screen reads a message for known scam patterns, by its words alone (no
// history): ok, warn, or hold for a message that asks for a code, or one
// that pairs a transfer or request for money with a prize, a fee or an
// investment.
func Screen(text string) Verdict {
	var r []string
	add := func(ok bool, reason string) {
		if ok {
			r = append(r, reason)
		}
	}
	add(AsksForTransfer(text), ReasonTransfer)
	add(sendMoneyRe.MatchString(text), ReasonSendMoney)
	add(prizeRe.MatchString(text), ReasonPrize)
	add(feeRe.MatchString(text), ReasonFee)
	add(codeRe.MatchString(text), ReasonCode)
	add(investmentRe.MatchString(text), ReasonInvestment)
	if len(r) == 0 {
		return Verdict{Verdict: VerdictOK}
	}
	add(linkRe.MatchString(text), ReasonLink)
	has := func(x string) bool {
		for _, y := range r {
			if y == x {
				return true
			}
		}
		return false
	}
	money := has(ReasonTransfer) || has(ReasonSendMoney)
	bait := has(ReasonPrize) || has(ReasonFee) || has(ReasonInvestment)
	if has(ReasonCode) || (money && bait) || (bait && has(ReasonLink)) {
		return Verdict{Verdict: VerdictHold, Reasons: r}
	}
	return Verdict{Verdict: VerdictWarn, Reasons: r}
}

// ScreenStranger checks a message from from to to, who has not accepted
// a chat with from (a request, or a new chat): Screen's verdict, made a
// hold when from already had RepeatHold warned or held messages to
// strangers in the last day. A warn or hold is kept (from, to, the
// verdict, the reasons, when); the words never are, nor anything for ok.
// The caller delivers it (ok), delivers it with the warning (warn), or
// holds it back (hold).
func (p *People) ScreenStranger(ctx context.Context, from, to uuid.UUID, text string) (Verdict, error) {
	v := Screen(text)
	if v.Verdict == VerdictOK {
		return v, nil
	}
	err := db.InPersonTx(ctx, p.Pool, from, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM people_screen_verdicts WHERE from_person = $1 AND at > $2`,
			from, p.now().Add(-24*time.Hour)).Scan(&n); err != nil {
			return err
		}
		if n >= RepeatHold && v.Verdict == VerdictWarn {
			v = Verdict{Verdict: VerdictHold, Reasons: append(v.Reasons, ReasonRepeat)}
		}
		var toID *uuid.UUID
		if to != uuid.Nil {
			toID = &to
		}
		_, err := tx.Exec(ctx, `INSERT INTO people_screen_verdicts (id, from_person, to_person, verdict, reasons, at) VALUES ($1, $2, $3, $4, $5, $6)`,
			uuid.Must(uuid.NewV7()), from, toID, v.Verdict, v.Reasons, p.now())
		return err
	})
	return v, err
}

// VerdictsSince counts a sender's kept verdicts since a time (for limits
// that slow a sender); in the sender's scope.
func (p *People) VerdictsSince(ctx context.Context, from uuid.UUID, since time.Time) (warn, hold int, err error) {
	err = db.InPersonTx(ctx, p.Pool, from, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE verdict = 'warn'), count(*) FILTER (WHERE verdict = 'hold')
			FROM people_screen_verdicts WHERE from_person = $1 AND at > $2`, from, since).Scan(&warn, &hold)
	})
	return
}
