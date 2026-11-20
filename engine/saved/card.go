package saved

import (
	"strings"
)

// A saved thing shared in a chat (saved-objects §6.2; S1 part 3): a copy of
// it as it was when sent, never a link, so changing the object later changes
// nothing already shared. It shows as a card that folds to a row and opens
// to its details, each with Copy, and Save this for the receiver; on
// WhatsApp, Telegram and Slack it goes as plain lines. Sharing anything
// sensitive (an account number, a phone, an email) is confirmed by the
// sender first.

// Card is a saved thing as it is shared.
type Card struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// Name is the sender's @name for it, for the receiver to keep or change.
	Name string `json:"name"`
	// Lines are its details as shown, each with its label.
	Lines []CardLine `json:"lines"`
	// Details are its fields as Save this keeps them: a bank account's
	// bank_name, bank_code and account_number in full; any other kind's
	// fields as Clean takes them.
	Details map[string]any `json:"details"`
	// Sensitive: it holds what the sender confirmed sharing.
	Sensitive bool `json:"sensitive"`
}

// CardLine is one detail of a card.
type CardLine struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// MakeCard is the card for an object of kind with its kept details and, for
// a bank account, its account number in full.
func MakeCard(kind, name, title string, details map[string]any, accountNumber string) Card {
	str := func(k string) string { v, _ := details[k].(string); return v }
	c := Card{Kind: kind, Title: title, Name: name, Details: map[string]any{}}
	line := func(label, key, value string) {
		if value == "" {
			return
		}
		c.Lines = append(c.Lines, CardLine{Label: label, Value: value})
		if key != "" {
			c.Details[key] = value
		}
	}
	switch kind {
	case KindBankAccount:
		line("Bank", "bank_name", str("bank_name"))
		line("Account number", "account_number", accountNumber)
		if code := str("bank_code"); code != "" {
			c.Details["bank_code"], c.Details["code_from"] = code, str("code_from")
		}
		c.Sensitive = accountNumber != ""
	case KindPerson:
		line("Phone", "phone", str("phone"))
		line("Email", "email", str("email"))
		line("Address", "address", str("address"))
		line("Birthday", "birthday", str("birthday"))
		c.Sensitive = str("phone") != "" || str("email") != ""
	case KindPlace:
		line("Address", "line", str("line"))
		line("Area", "area", str("area"))
		line("Landmark", "landmark", str("landmark"))
		line("Phone for the rider", "phone", str("phone"))
		c.Sensitive = str("phone") != ""
	case KindText:
		line("Text", "body", str("body"))
	case KindProviderAccount:
		line(ProviderWord(str("type")), "", str("provider"))
		line("Number", "number", str("number"))
		line("Name on it", "name_on_it", str("name_on_it"))
		c.Details["type"], c.Details["provider"] = str("type"), str("provider")
	}
	return c
}

// Plain is a card as plain lines, for WhatsApp, Telegram and Slack: the
// title, then each detail on its own line, easy to copy.
func (c Card) Plain() string {
	var b strings.Builder
	b.WriteString(c.Title)
	for _, l := range c.Lines {
		b.WriteString("\n")
		if l.Label != "" && l.Label != "Text" {
			b.WriteString(l.Label)
			b.WriteString(": ")
		}
		b.WriteString(l.Value)
	}
	return b.String()
}

// Check refuses a card sent by a client rather than made here: Kind must be
// a kind, and the title and lines within their limits. Cards are made by
// the server from a saved thing (MakeCard); this guards one read back.
func (c Card) Check() error {
	switch {
	case !knownKind(c.Kind):
		return invalid("That is not a kind of saved thing.")
	case c.Title == "" || len([]rune(c.Title)) > MaxTitle:
		return invalid("A shared thing has a title.")
	case len(c.Lines) > 12:
		return invalid("A shared thing has too many lines.")
	}
	for _, l := range c.Lines {
		if len([]rune(l.Value)) > MaxText || len([]rune(l.Label)) > 40 {
			return invalid("A shared thing's line is too long.")
		}
	}
	return nil
}

func knownKind(k string) bool {
	for _, x := range Kinds {
		if x == k {
			return true
		}
	}
	return false
}
