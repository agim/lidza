//go:build evals

package evals

import "testing"

// The browser's own controls in a page are L026 warnings in an app that
// has src/ui.tsx; the app's Select and Dialog are not.
func TestNativeControls(t *testing.T) {
	edit(t, map[string]string{
		"src/pages/Native.tsx": `import { useState } from 'react'
import { Dialog, Select } from '../ui'

export function Native() {
  const [status, setStatus] = useState('draft')
  const [open, setOpen] = useState(false)
  const remove = () => {
    if (!confirm('Delete?')) return
  }
  return (
    <div>
      <select value={status} onChange={(e) => setStatus(e.target.value)}>
        <option value="draft">Draft</option>
      </select>
      <dialog open={open}>Old</dialog>
      <Select label="Status" value={status} onChange={setStatus} options={[{ value: 'draft', label: 'Draft' }]} />
      <Dialog open={open} onClose={() => setOpen(false)} title="Delete?" />
      <button type="button" onClick={remove}>Delete</button>
    </div>
  )
}
`,
	})
	r := check(t)
	expect(t, r,
		expectation{code: "L026", severity: "warning", file: "src/pages/Native.tsx", message: "browser's dropdown"},
		expectation{code: "L026", severity: "warning", file: "src/pages/Native.tsx", message: "<dialog> written by hand"},
		expectation{code: "L026", severity: "warning", file: "src/pages/Native.tsx", message: "browser's own dialog"},
	)
	n := 0
	for _, d := range r.Diagnostics {
		if d.Code == "L026" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("%d L026 findings, want 3:\n%s", n, dump(r))
	}
}
