package credentials

import (
	"bytes"
	"maps"
	"testing"
)

// FuzzParse: the credentials file is text anyone with the repository
// can edit. Reading it never panics, and what Format writes reads back
// the same.
func FuzzParse(f *testing.F) {
	f.Add("A: 1\nB: \"x\\ny\"\n# c\n")
	f.Add("production:\n  KEY: v\n")
	f.Add("\"unterminated")
	f.Fuzz(func(t *testing.T, text string) {
		values, err := Parse(text)
		if err != nil {
			return
		}
		again, err := Parse(Format(values))
		if err != nil {
			t.Fatalf("Format output does not parse: %v\n%s", err, Format(values))
		}
		if !maps.Equal(values, again) {
			t.Fatalf("round trip changed values: %q -> %q", values, again)
		}
	})
}

// FuzzDecrypt: sealed values come from a file; a damaged one is an
// error, never a panic, and what Encrypt seals opens.
func FuzzDecrypt(f *testing.F) {
	key := bytes.Repeat([]byte{7}, 32)
	sealed, _ := Encrypt(key, []byte("secret"))
	f.Add(sealed, []byte("plain"))
	f.Add("", []byte(nil))
	f.Add("AAAA", []byte{0})
	f.Fuzz(func(t *testing.T, s string, plain []byte) {
		Decrypt(key, s)
		Decrypt(plain, s)
		out, err := Encrypt(key, plain)
		if err != nil {
			t.Fatal(err)
		}
		back, err := Decrypt(key, out)
		if err != nil || !bytes.Equal(back, plain) {
			t.Fatalf("round trip: %q %v", back, err)
		}
	})
}
