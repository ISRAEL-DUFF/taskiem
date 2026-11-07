package inmail

import "github.com/israel-duff/taskiem/engine/mailtext"

// TextFromHTML is an HTML body as plain text (mailtext.TextFromHTML).
func TextFromHTML(s string) string { return mailtext.TextFromHTML(s) }

// Latest is the newest part of a reply, without the quoted earlier
// messages (mailtext.Latest).
func Latest(text string) string { return mailtext.Latest(text) }
