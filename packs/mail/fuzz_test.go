package mail

import (
	"strings"
	"testing"
)

func FuzzParseAcceptLanguage(f *testing.F) {
	for _, s := range []string{"de-CH, fr;q=0.8, *;q=0.1", "en;q=NaN, fr;q=1e400", ",,,;q=", strings.Repeat("en,", 50)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, header string) {
		tags := ParseAcceptLanguage(header)
		if len(tags) > 10 {
			t.Fatalf("%d tags", len(tags))
		}
		for _, tag := range tags {
			if tag == "" || tag == "*" || len(tag) > 35 {
				t.Fatalf("tag %q", tag)
			}
		}
	})
}

// FuzzValidateMessage: addresses come from forms. A message that passes
// carries no line break in any address header, so no header can be
// injected.
func FuzzValidateMessage(f *testing.F) {
	f.Add("ann@example.com", "Shop <shop@example.com>", "a@example.com, b@example.com", "c@example.com")
	f.Add("x@example.com\r\nBcc: y@example.com", "", "", "")
	f.Add("\"a\\\r\n\" <a@example.com>", "f@example.com", "", "")
	f.Fuzz(func(t *testing.T, to, from, replyTo, cc string) {
		msg := &Message{To: to, From: from, ReplyTo: replyTo, Subject: "s", Text: "t"}
		if cc != "" {
			msg.Cc = []string{cc}
		}
		if validateMessage(msg, Config{MaxRecipients: 50}) != nil {
			return
		}
		for _, v := range []string{to, from, replyTo, cc} {
			if strings.ContainsAny(v, "\r\n") {
				t.Fatalf("accepted a line break in %q", v)
			}
		}
	})
}
