package diag

import "testing"

func TestFormTargets(t *testing.T) {
	src := `<form action="mailto:someone@example.com" method="post">
<a href="mailto:someone@example.com">write to us</a>
<form action="/contact" method="post"><button formaction="http://old.example.com/send">Send</button></form>
<form action="https://forms.example.com/x"></form>
<form
  onSubmit={(e) => send(e)}
  action={"mailto:x@example.com"}>
</form>
<form action="#contact"></form>
<form action="{{.Action}}"></form>
<!-- lidza:ignore L022 -->
<form action="mailto:kept@example.com"></form>
`
	got := checkFormTargets("src/Contact.tsx", src)
	lines := map[int]bool{}
	for _, d := range got {
		if d.Code != "L022" {
			t.Fatalf("%+v", d)
		}
		lines[d.Line] = true
	}
	if len(got) != 3 || !lines[1] || !lines[3] || !lines[7] {
		t.Fatalf("findings: %+v", got)
	}
}
