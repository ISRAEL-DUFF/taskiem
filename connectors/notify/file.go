package notify

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/sender"
)

// A business's saved file sent with a message (build plan H6). A run reads
// a saved file as a reference to it (saved.price_list.file is
// "saved-file:<id>"), never a link: a message step given it
// (file: =saved.price_list) gets a short link made when the message is
// sent (FileLinks), so a late or resumed step's link is fresh and the
// run's history holds nothing that opens the file. The link goes after
// the message, room kept for it wherever the message is cut: the SMS's
// three parts, the chat templates' one line.

// FileRefPrefix starts a saved file's reference in a run.
const FileRefPrefix = "saved-file:"

// ErrNoFile is a saved file a message cannot send: not the business's,
// not one its run read, removed, or kept nowhere.
var ErrNoFile = errors.New("the saved file is not there any more")

// ErrFilesOff is a Taskiem that cannot send saved files (it has no
// address of its own for the links).
var ErrFilesOff = errors.New("saved files cannot be sent from this Taskiem")

// ErrFileTooLong is a saved file's line that does not fit in a message.
var ErrFileTooLong = errors.New("the saved file's link does not fit in this message")

// FileGone reports whether err says a message's saved file can never be
// sent (so the message is not tried again for it); any other error may
// pass, and the message is tried again.
func FileGone(err error) bool {
	return errors.Is(err, ErrNoFile) || errors.Is(err, ErrFilesOff) || errors.Is(err, ErrFileTooLong) || errors.Is(err, effects.ErrFatal)
}

// FileLinks makes links to a business's saved files for the messages that
// send them.
type FileLinks interface {
	// FileLink is the saved file ref's name and a link to it, good for a
	// week, for a message of run (the file must be one the run read): in
	// tx when given (the business's scope), else in a transaction of its
	// own. ErrNoFile when the run read no such file of the business's.
	FileLink(ctx context.Context, tx pgx.Tx, tenant, run uuid.UUID, ref string) (name, link string, err error)
}

// fileExpr is a file written as the saved root: =saved.price_list,
// =saved["price-list"], =saved.price_list.file.
var fileExpr = regexp.MustCompile(`^=\s*saved(\.[a-z0-9_]+|\[\s*["'][a-z0-9-]+["']\s*\])(\.file)?\s*$`)

// checkFile holds a message's file to one of the business's saved files,
// written as the saved root (=saved.price_list): anything else, a link
// written out or another expression, is no saved file.
func checkFile(input map[string]any) []string {
	raw, has := input["file"]
	if !has {
		return nil
	}
	if s, ok := raw.(string); ok && fileExpr.MatchString(s) {
		return nil
	}
	return []string{"file is one of the business's saved files, written =saved.price_list"}
}

// FileRef is the saved file a step's input sends, as the run read it ("",
// none); a value that is not a saved file's reference is refused for good.
func FileRef(input map[string]any) (string, error) {
	raw, has := input["file"]
	if !has || raw == nil {
		return "", nil
	}
	var ref string
	switch v := raw.(type) {
	case string:
		ref = v
	case map[string]any:
		ref, _ = v["file"].(string)
	}
	ref = strings.TrimSpace(ref)
	id, ok := strings.CutPrefix(ref, FileRefPrefix)
	if _, err := uuid.Parse(id); !ok || err != nil {
		return "", fmt.Errorf("file is one of the business's saved files, written =saved.price_list: %w", effects.ErrFatal)
	}
	return ref, nil
}

// FileLine is the line a saved file adds to a message: its name and link.
func FileLine(name, link string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "The file"
	}
	return name + ": " + link
}

// WithFileLine is message with line after it ("" leaves it as it is).
func WithFileLine(message, line string) string {
	if line == "" {
		return message
	}
	return strings.TrimRight(message, "\n") + "\n\n" + line
}

// CutFor is text cut, with "…", to at most max characters with room kept
// for line after it (joined by sep); text and line joined. An error when
// the line alone does not fit.
func CutFor(text, sep, line string, max int) (string, error) {
	if line == "" {
		if r := []rune(text); len(r) > max {
			return string(r[:max]) + "…", nil
		}
		return text, nil
	}
	room := max - len([]rune(sep+line)) - 1
	if room < 1 {
		return "", errFileTooLong
	}
	if r := []rune(text); len(r) > room {
		text = string(r[:room]) + "…"
	}
	return text + sep + line, nil
}

var errFileTooLong = fmt.Errorf("%w: %w", ErrFileTooLong, effects.ErrFatal)

// SMSTextWithFile is SMSText with a saved file's line kept whole after
// the message (cut to make room); an error when the line alone does not
// fit in the SMS's parts.
func SMSTextWithFile(business, title, message, line string) (string, error) {
	if line == "" {
		return SMSText(business, title, message), nil
	}
	name := sender.CleanName(business)
	if sender.Segments(name+": "+line) > sender.MaxSegments {
		return "", errFileTooLong
	}
	t := message
	if title != "" {
		t = title + ": " + message
	}
	whole := func(s string) string { return s + "\n" + line }
	if sender.Segments(name+": "+whole(t)) <= sender.MaxSegments {
		return whole(t), nil
	}
	r := []rune(t)
	for len(r) > 0 && sender.Segments(name+": "+whole(string(r)+"…")) > sender.MaxSegments {
		r = r[:len(r)-min(10, len(r))]
	}
	if len(r) == 0 {
		return line, nil
	}
	return whole(string(r) + "…"), nil
}

// fileLineFor is the line a message's saved file adds, its link made now
// in tx, the business's (none without a file).
func (n *Notifier) fileLineFor(ctx context.Context, tx pgx.Tx, tenant, run uuid.UUID, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	if n.Files == nil {
		return "", fmt.Errorf("%w: %w", ErrFilesOff, effects.ErrFatal)
	}
	name, link, err := n.Files.FileLink(ctx, tx, tenant, run, ref)
	if FileGone(err) {
		return "", fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		return "", fmt.Errorf("%w: %w", effects.ErrRetryable, err)
	}
	return FileLine(name, link), nil
}
