package forms

import "time"

// What the form host writes to the inbox, sealed with Keys.Seal under
// SubmissionAAD(reference).

// Submission is one completed form.
type Submission struct {
	Answers    map[string]any `json:"answers"`
	Respondent Respondent     `json:"respondent"`
	// Channel is direct or embed (app for members, which the API takes).
	Channel string `json:"channel"`
	// Site is the embedding website, when embedded.
	Site     string    `json:"site,omitempty"`
	Consents []Consent `json:"consents,omitempty"`
	// Files are the uploads, in form_uploads in this order, each sealed
	// under UploadAAD(reference, field, n) with n counting that field's.
	Files     []FileMeta `json:"files,omitempty"`
	StatusKey string     `json:"status_key,omitempty"`
	Submitted time.Time  `json:"submitted_at"`
}

// Respondent is who filled it in (forms 8.2).
type Respondent struct {
	// Kind is anonymous, verified (a code), link (a one-time link, perhaps
	// also verified), request (a form step's link) or member.
	Kind     string `json:"kind"`
	Phone    string `json:"phone,omitempty"`
	Email    string `json:"email,omitempty"`
	MemberID string `json:"member_id,omitempty"`
}

// Consent is a consent field agreed to: its text's hash and when.
type Consent struct {
	Field    string    `json:"field"`
	TextHash string    `json:"text_hash"`
	At       time.Time `json:"at"`
}

// FileMeta is an upload as the respondent sent it.
type FileMeta struct {
	Field string `json:"field"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Size  int64  `json:"size"`
}

// FileRef is a file field's value in the run and the submission (forms
// 6.5): the content stays in storage, read through form@1 file_url.
type FileRef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// CodeRequest is a verification code to send, sealed under CodeAAD(scope).
// Address is empty for a form step's request: the code goes to its
// recipient.
type CodeRequest struct {
	Channel string `json:"channel"` // sms or email
	Address string `json:"address,omitempty"`
	Code    string `json:"code"`
}

// RequestView is what the form host may show of a form step's request,
// sealed under RequestViewAAD(id) by the api role when it sends the link:
// the prefill as the run had it, and a hint of who it is for.
type RequestView struct {
	Prefill map[string]any `json:"prefill,omitempty"`
	// Channel and Hint ("•••4567", "a•••@example.com") say where a code
	// goes when the request asks for one.
	Channel string `json:"channel,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// LinkView is what the form host may show of a one-time link bound to a
// person: where its code goes. Address is the normalised address the
// respondent must verify (only the form host and the api role can open
// it).
type LinkView struct {
	Channel string `json:"channel"`
	Address string `json:"address"`
	Hint    string `json:"hint"`
}
