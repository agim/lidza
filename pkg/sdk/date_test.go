package sdk

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/inspect"
	"github.com/agim/lidza/pkg/schema"
)

const eventSchema = `type CreateEvent {
  title   string
  day     date
  endsOn  date?
  startAt localtime
  zone    string @timezone
}
`

// TestDateClients: a date, a localtime and a @timezone field are strings
// in the client, documented, and validators.ts checks them as the server
// does; with tsc and node the validator runs.
func TestDateClients(t *testing.T) {
	s, err := schema.Parse(eventSchema)
	if err != nil {
		t.Fatal(err)
	}
	c := &inspect.Context{
		App:        inspect.App{Name: "demo"},
		Operations: []inspect.Operation{{ID: "createEvent", Method: "POST", Path: "/api/v1/events", Params: []string{}, Input: "CreateEvent", Output: "CreateEvent"}},
		Schemas:    schema.JSONSchema(s),
	}
	files := TypeScript(c)
	for _, want := range []string{
		"  /** Calendar day, \"2026-10-06\": no time zone, never shift it. */\n  day: string\n",
		"  /** Local date and time as typed, \"2026-10-06T10:30\" (no zone): the server reads it in the visitor's zone. */\n  startAt: string\n",
	} {
		if !strings.Contains(files["types.ts"], want) {
			t.Errorf("types.ts lacks %q:\n%s", want, files["types.ts"])
		}
	}
	tsc := findTSC(t)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	dir := t.TempDir()
	writeClient(t, filepath.Join(dir, "client"), c)
	opts := templateOptions(t)
	opts["noEmit"] = false
	opts["outDir"] = "js"
	opts["module"] = "node16"
	opts["moduleResolution"] = "node16"
	opts["rootDir"] = "client"
	opts["verbatimModuleSyntax"] = false
	runTSC(t, tsc, dir, opts, []string{"client"})
	script := `const { validateCreateEvent } = require('./js/index.js')
const ok = { title: 'x', day: '2026-10-06', startAt: '2026-10-06T10:30', zone: 'Europe/Paris' }
const cases = [ok, { ...ok, day: '' }, { ...ok, day: '2026-02-30' }, { ...ok, day: '06/10/2026' },
  { ...ok, startAt: '2026-10-06T10:30Z' }, { ...ok, startAt: '' }, { ...ok, zone: 'Mars/Olympus' }, { ...ok, endsOn: '2026-13-01' }]
console.log(cases.map((c) => validateCreateEvent(c).map((e) => e.field + ':' + e.rule).join(' ')).join('|'))
`
	os.WriteFile(filepath.Join(dir, "main.js"), []byte(script), 0o644)
	cmd := exec.Command(node, "main.js")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	want := "|day:required|day:date|day:date|startAt:pattern|startAt:required|zone:timezone|endsOn:date"
	if got := strings.TrimSpace(string(out)); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
