package brief

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Palette is a set of design tokens an answer to "Which palette?" writes
// into the app: the Tailwind tokens of src/index.css (react) and the admin
// pages' theme (admin/theme.css), light and dark.
type Palette struct {
	Name, Detail string
	// Light and Dark are the admin theme's --admin-* values.
	Light, Dark map[string]string
	// Tokens are the frontend's --color-* values.
	Tokens map[string]string
}

// Palettes are the suggestions; none is a stock blue.
var Palettes = []Palette{
	{Name: "Mulberry and paper", Detail: "Līdza's own: a berry accent on warm neutrals",
		Light:  map[string]string{"accent": "oklch(47% 0.13 345)", "accent-fg": "#ffffff", "bg": "#f6f2ed", "surface": "#fffdfa", "surface-2": "#f1ebe4", "line": "#e4dbd1", "fg": "#2a2224", "muted": "#7b6f69", "sidebar": "#241821"},
		Dark:   map[string]string{"accent": "oklch(76% 0.1 345)", "accent-fg": "#221519", "bg": "#171214", "surface": "#201a1d", "surface-2": "#2a2226", "line": "#372d32", "fg": "#eee7e3", "muted": "#a89a93"},
		Tokens: map[string]string{"brand": "oklch(47% 0.13 345)", "brand-strong": "oklch(40% 0.13 345)", "ink": "#2a2224", "muted": "#7b6f69", "surface": "#f8f4ef", "line": "#e4dbd1"}},
	{Name: "Forest and linen", Detail: "a deep green on off-white linen",
		Light:  map[string]string{"accent": "oklch(45% 0.09 160)", "accent-fg": "#ffffff", "bg": "#f4f3ee", "surface": "#fdfcf8", "surface-2": "#ecebe3", "line": "#deddd2", "fg": "#1f2622", "muted": "#6b726c", "sidebar": "#17241e"},
		Dark:   map[string]string{"accent": "oklch(75% 0.09 160)", "accent-fg": "#10201a", "bg": "#121714", "surface": "#1a201c", "surface-2": "#222a25", "line": "#2e3832", "fg": "#e6ebe7", "muted": "#99a39c"},
		Tokens: map[string]string{"brand": "oklch(45% 0.09 160)", "brand-strong": "oklch(38% 0.09 160)", "ink": "#1f2622", "muted": "#6b726c", "surface": "#f6f5f0", "line": "#deddd2"}},
	{Name: "Ink and saffron", Detail: "near-black ink with a saffron accent",
		Light:  map[string]string{"accent": "oklch(72% 0.16 70)", "accent-fg": "#1d1a16", "bg": "#f7f5f0", "surface": "#ffffff", "surface-2": "#efece5", "line": "#e2ddd3", "fg": "#1d1d22", "muted": "#6d6b70", "sidebar": "#16161b"},
		Dark:   map[string]string{"accent": "oklch(78% 0.15 75)", "accent-fg": "#1d1a16", "bg": "#141417", "surface": "#1c1c21", "surface-2": "#25252b", "line": "#33333a", "fg": "#ececf0", "muted": "#9d9ca3"},
		Tokens: map[string]string{"brand": "oklch(62% 0.16 65)", "brand-strong": "oklch(54% 0.15 60)", "ink": "#1d1d22", "muted": "#6d6b70", "surface": "#f7f5f0", "line": "#e2ddd3"}},
	{Name: "Deep teal and sand", Detail: "a sea teal on sand",
		Light:  map[string]string{"accent": "oklch(48% 0.08 195)", "accent-fg": "#ffffff", "bg": "#f5f2ec", "surface": "#fffdf9", "surface-2": "#ede8df", "line": "#e0d9cc", "fg": "#1f2426", "muted": "#6c7275", "sidebar": "#142326"},
		Dark:   map[string]string{"accent": "oklch(76% 0.08 195)", "accent-fg": "#0f1c1e", "bg": "#111719", "surface": "#192023", "surface-2": "#212a2d", "line": "#2c373a", "fg": "#e5ecee", "muted": "#97a3a6"},
		Tokens: map[string]string{"brand": "oklch(48% 0.08 195)", "brand-strong": "oklch(41% 0.08 195)", "ink": "#1f2426", "muted": "#6c7275", "surface": "#f7f4ee", "line": "#e0d9cc"}},
}

// OwnBrand is the suggestion for an app that has its colours already.
const OwnBrand = "Our own brand colours"

func paletteSuggestions() []Suggestion {
	out := make([]Suggestion, 0, len(Palettes)+1)
	for _, p := range Palettes {
		out = append(out, Suggestion{p.Name, p.Detail})
	}
	return append(out, Suggestion{OwnBrand, "give the hex values; the agent writes the tokens"})
}

// paletteFor finds the palette an answer names.
func paletteFor(answer string) (Palette, bool) {
	for _, p := range Palettes {
		if strings.EqualFold(strings.TrimSpace(answer), p.Name) || strings.HasPrefix(strings.ToLower(answer), strings.ToLower(p.Name)) {
			return p, true
		}
	}
	return Palette{}, false
}

// themeMarker marks an admin/theme.css the brief wrote, which a later
// answer may replace; a file without it is the app's own and stays.
const themeMarker = "/* Written by lidza brief from the palette answer; edit freely. */"

// applyPalette writes the palette into the frontend's tokens and the
// admin theme; it returns the files changed and the ones it left for the
// agent (a template without tokens, an admin theme the app wrote).
func applyPalette(dir string, p Palette) (changed, manual []string, err error) {
	css := filepath.Join(dir, "src", "index.css")
	if data, rerr := os.ReadFile(css); rerr == nil && strings.Contains(string(data), "@theme") {
		s := string(data)
		for k, v := range p.Tokens {
			re := regexp.MustCompile(`(--color-` + regexp.QuoteMeta(k) + `:\s*)[^;]+;`)
			s = re.ReplaceAllString(s, "${1}"+v+";")
		}
		if s != string(data) {
			if err := os.WriteFile(css, []byte(s), 0o644); err != nil {
				return nil, nil, err
			}
			changed = append(changed, "src/index.css")
		}
	} else {
		manual = append(manual, "the frontend's stylesheet (no @theme tokens in src/index.css)")
	}
	theme := filepath.Join(dir, "admin", "theme.css")
	if data, rerr := os.ReadFile(theme); rerr == nil && !strings.Contains(string(data), themeMarker) {
		manual = append(manual, "admin/theme.css (the app's own)")
		return changed, manual, nil
	}
	var b strings.Builder
	b.WriteString(themeMarker + "\n/* " + p.Name + ": " + p.Detail + ". */\n:root,\n[data-bs-theme=light] {\n")
	for _, k := range []string{"accent", "accent-fg", "bg", "surface", "surface-2", "line", "fg", "muted", "sidebar"} {
		fmt.Fprintf(&b, "  --admin-%s: %s;\n", k, p.Light[k])
	}
	b.WriteString("}\n[data-bs-theme=dark] {\n")
	for _, k := range []string{"accent", "accent-fg", "bg", "surface", "surface-2", "line", "fg", "muted"} {
		fmt.Fprintf(&b, "  --admin-%s: %s;\n", k, p.Dark[k])
	}
	b.WriteString("}\n")
	if err := os.MkdirAll(filepath.Dir(theme), 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(theme, []byte(b.String()), 0o644); err != nil {
		return nil, nil, err
	}
	return append(changed, "admin/theme.css"), manual, nil
}
