package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebRules(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	write("package.json", `{"dependencies":{"react":"1","@tanstack/react-query":"1"},"devDependencies":{"vite":"1"}}`)
	write("src/pages/Things.tsx", `import { useQuery } from '@tanstack/react-query'
import { api } from '@lidza/client'
import { Thing } from './Thing'
import dayjs from 'dayjs'
import type { X } from '@scope/missing/sub'
import 'virtual:something'
import '/absolute.css'
export { y } from 'react/jsx-runtime'
const mod = await import('lodash-es')

export function Things() {
  const q = useQuery({ queryKey: ['t'], queryFn: () => fetch('/api/v1/things').then((r) => r.json()) })
  // lidza:ignore L003
  fetch("/api/v1/allowed")
  fetch('https://example.com/api')
  return null
}
`)
	write("src/report.ts", "// lidza:ignore L004 vendored\nimport x from 'vendored'\n")
	got := WebRules(dir)
	var lines []string
	for _, d := range got {
		lines = append(lines, d.Code+" "+d.File+":"+itoa(d.Line)+" "+d.Severity)
	}
	want := "L004 src/pages/Things.tsx:4 error,L004 src/pages/Things.tsx:5 error,L004 src/pages/Things.tsx:9 error,L003 src/pages/Things.tsx:12 warning"
	if strings.Join(lines, ",") != want {
		t.Fatalf("got %v\nwant %s", lines, want)
	}
	if !strings.Contains(got[1].Message, "@scope/missing") {
		t.Errorf("scoped name: %s", got[1].Message)
	}
	if WebRules(t.TempDir()) != nil {
		t.Error("no src")
	}
}
