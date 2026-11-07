package inmail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestThreadKey(t *testing.T) {
	for in, want := range map[string]string{"T-1042": "T-1042", " t-1042 ": "T-1042", "ab12": "AB12", "S-2026-07": "S-2026-07"} {
		if got, ok := ThreadKey(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "T--1", "-T1", "T1-", "T 1", "T_1", "[#T-1]", strings.Repeat("A", 25), "T-10\n42"} {
		if got, ok := ThreadKey(in); ok {
			t.Errorf("%q is taken as %q", in, got)
		}
	}
}

func TestTag(t *testing.T) {
	for in, want := range map[string]string{"bank": "bank", "T-1042-ABC": "t-1042-abc", "a": "a"} {
		if got := Tag(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	for _, in := range []string{"", "-a", "a-", "a.b", "a_b", "a+b", strings.Repeat("a", MaxTag+1)} {
		if got := Tag(in); got != "" {
			t.Errorf("%q is taken as %q", in, got)
		}
	}
}

func TestReplyTags(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	ep := uuid.New()
	tag := ReplyTag(key, ep, "T-1042")
	if !strings.HasPrefix(tag, "t-1042-") || len(tag) != len("t-1042-")+tokenLen || Tag(tag) != tag {
		t.Fatalf("tag %q", tag)
	}
	if got, ok := VerifyTag(key, ep, tag); !ok || got != "T-1042" {
		t.Fatalf("its own tag: %q %v", got, ok)
	}
	// Forged: the next ticket's number with this one's token, a token
	// altered, another address's tag, another secret, no secret yet, and
	// shapes that are not tags at all.
	token := tag[len("t-1042-"):]
	other := ReplyTag(key, uuid.New(), "T-1042")
	for name, c := range map[string]struct {
		key []byte
		tag string
	}{
		"guessed ticket": {key, "t-1043-" + token},
		"altered token":  {key, "t-1042-" + strings.Repeat("a", tokenLen)},
		"other address":  {key, other},
		"other secret":   {bytes.Repeat([]byte{8}, 32), tag},
		"no secret":      {nil, tag},
		"bare key":       {key, "t-1042"},
		"short token":    {key, "t-1042-" + token[:8]},
		"no key":         {key, "-" + token},
	} {
		if got, ok := VerifyTag(c.key, ep, c.tag); ok {
			t.Errorf("%s: %q verifies as %q", name, c.tag, got)
		}
	}
	// The same thread always has the same tag; another thread another.
	if ReplyTag(key, ep, "T-1042") != tag || ReplyTag(key, ep, "T-1043")[len("t-1043-"):] == token {
		t.Error("tags are not stable per thread")
	}
}

func TestThreadMessageID(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	ep := uuid.New()
	a := ThreadMessageID(key, ep, "tsk_aaaa", "in.taskiem.test")
	if a != ThreadMessageID(key, ep, "tsk_aaaa", "in.taskiem.test") || !strings.HasPrefix(a, "tk-") || !strings.HasSuffix(a, "@in.taskiem.test") || !ValidMessageID(a) {
		t.Fatalf("%q", a)
	}
	if a == ThreadMessageID(key, ep, "tsk_bbbb", "in.taskiem.test") || a == ThreadMessageID(bytes.Repeat([]byte{8}, 32), ep, "tsk_aaaa", "in.taskiem.test") {
		t.Error("message ids repeat across steps or secrets")
	}
}

func TestSubjectTags(t *testing.T) {
	for in, want := range map[string]string{
		"Re: [#T-1042] Your order": "T-1042",
		"[#t-7] hi":                "T-7",
		"Your order":               "",
		"[T-1042]":                 "",
		"[# T-1042]":               "",
	} {
		if got := SubjectThread(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	for in, want := range map[string]string{
		"Your order":               "[#T-1042] Your order",
		"Re: [#T-1042] Your order": "Re: [#T-1042] Your order",
		"[#t-1042] Your order":     "[#t-1042] Your order",
		"":                         "[#T-1042]",
		"[#T-9] Other":             "[#T-1042] [#T-9] Other",
	} {
		if got := TagSubject(in, "T-1042"); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestMessageIDs(t *testing.T) {
	cases := map[string][]string{
		"<a@x.example>": {"a@x.example"},
		" <a@x.example> (comment) <b@y.example>\r\n <a@x.example>": {"a@x.example", "b@y.example"},
		"a@x.example b@y.example":                                  {"a@x.example", "b@y.example"},
		"Your message of Monday":                                   {},
		"<no-at-sign> <has space@x> <ok@x>":                        {"ok@x"},
		"<" + strings.Repeat("a", 260) + "@x>":                     {},
		"<a@x.example":                                             {},
		"":                                                         {},
	}
	for in, want := range cases {
		got := MessageIDs(in)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestParseThreadingHeaders(t *testing.T) {
	var refs []string
	for i := range 30 {
		refs = append(refs, fmt.Sprintf("<r%d@x.example>", i))
	}
	raw := []byte(strings.ReplaceAll(`From: Ada <ada@x.example>
To: k3x9q2m7a1bz+t-1-abcdefghijkl@in.taskiem.test
Subject: Re: [#T-1] Your order
Message-ID: <reply-1@x.example>
In-Reply-To: <tk-1@in.taskiem.test> <ignored@x.example>
References: `+strings.Join(refs, "\n ")+`
Content-Type: text/plain

Yes please.

On Monday, Ada's Kitchen wrote:
> On its way.
`, "\n", "\r\n"))
	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.InReplyTo != "tk-1@in.taskiem.test" {
		t.Errorf("in-reply-to %q", m.InReplyTo)
	}
	if len(m.References) != maxReferences || m.References[0] != "r10@x.example" || m.References[maxReferences-1] != "r29@x.example" {
		t.Errorf("references %q", m.References)
	}
	b := Body(m, nil, nil)
	raw2, _ := json.Marshal(b)
	var got struct {
		InReplyTo  string   `json:"in_reply_to"`
		References []string `json:"references"`
		Latest     string   `json:"latest"`
	}
	_ = json.Unmarshal(raw2, &got)
	if got.InReplyTo != "tk-1@in.taskiem.test" || len(got.References) != maxReferences || got.Latest != "Yes please." {
		t.Errorf("body: %s", raw2)
	}
	// A message with neither: an empty id and an empty list, so
	// expressions can read them.
	m, _ = Parse([]byte("From: a@x.example\r\nSubject: hi\r\n\r\nhi\r\n"))
	if b := Body(m, nil, nil); b["in_reply_to"] != "" || len(b["references"].([]any)) != 0 {
		t.Errorf("no threading: %v %v", b["in_reply_to"], b["references"])
	}
}

func TestThreadReferences(t *testing.T) {
	got := threadReferences([]string{"c0@x", "c1@x"}, []string{"t2@x", "t1@x"}, "c1@x")
	if fmt.Sprint(got) != "[c0@x c1@x t1@x t2@x]" {
		t.Errorf("%q", got)
	}
	var many []string
	for i := range 30 {
		many = append(many, fmt.Sprintf("m%d@x", i))
	}
	got = threadReferences(many, nil, "last@x")
	if len(got) != maxReferences || got[0] != "m0@x" || got[len(got)-1] != "last@x" {
		t.Errorf("bounded: %q", got)
	}
}
