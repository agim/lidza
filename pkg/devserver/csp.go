package devserver

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"

	"github.com/agim/lidza/pkg/middleware"
)

var (
	inlineScript = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
	inlineStyle  = regexp.MustCompile(`(?is)<style\b[^>]*>(.*?)</style\s*>`)
	attrSrc      = regexp.MustCompile(`(?i)\ssrc\s*=`)
	attrType     = regexp.MustCompile(`(?i)\stype\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
)

// allowInline adds the hash of each inline script and <style> element of
// page to the Content-Security-Policy already set on h, so a strict
// policy (middleware.DefaultCSP) holds for the build's own inline code:
// the router's hydration payload a prerendered or server-rendered page
// carries, a small script a bundler inlined. Only these exact bytes run;
// anything injected later does not match. Data blocks (JSON-LD, the i18n
// catalog) are not executed and need no hash. A directive that allows
// 'unsafe-inline' or is 'none' is left as it is: a hash would switch the
// first off and open the second.
func allowInline(h http.Header, page []byte) {
	policy := h.Get("Content-Security-Policy")
	if policy == "" {
		return
	}
	var scripts, styles []string
	for _, m := range inlineScript.FindAllSubmatch(page, -1) {
		if attrSrc.Match(m[1]) || !executable(m[1]) {
			continue
		}
		scripts = append(scripts, cspHash(m[2]))
	}
	for _, m := range inlineStyle.FindAllSubmatch(page, -1) {
		styles = append(styles, cspHash(m[1]))
	}
	for _, d := range []struct {
		name   string
		hashes []string
	}{{"script-src", scripts}, {"style-src", styles}} {
		if len(d.hashes) == 0 || !hashable(policy, d.name) {
			continue
		}
		policy = middleware.AddCSP(policy, d.name, d.hashes...)
	}
	h.Set("Content-Security-Policy", policy)
}

// executable reports whether a script element with these attributes runs
// (and so is governed by script-src): no type, a JavaScript type, a
// module or an import map. Anything else is a data block.
func executable(attrs []byte) bool {
	m := attrType.FindSubmatch(attrs)
	if m == nil {
		return true
	}
	t := strings.ToLower(strings.TrimSpace(string(m[1]) + string(m[2]) + string(m[3])))
	switch t {
	case "", "module", "importmap", "text/javascript", "application/javascript", "application/ecmascript", "text/ecmascript":
		return true
	}
	return false
}

// hashable reports whether hashes may be added to directive: the
// directive, else default-src, restricts it, and neither allows
// 'unsafe-inline' nor is 'none' (or empty, which is the same).
func hashable(policy, directive string) bool {
	sources := directiveSources(policy, directive)
	if sources == nil {
		sources = directiveSources(policy, "default-src")
	}
	if len(sources) == 0 {
		return false // unrestricted (nil), or nothing allowed (empty)
	}
	for _, s := range sources {
		if s == "'unsafe-inline'" || s == "'none'" {
			return false
		}
	}
	return true
}

// directiveSources is the source list of directive in policy, nil when
// the policy lacks it.
func directiveSources(policy, directive string) []string {
	for _, d := range strings.Split(policy, ";") {
		f := strings.Fields(d)
		if len(f) > 0 && strings.EqualFold(f[0], directive) {
			return append([]string{}, f[1:]...)
		}
	}
	return nil
}

// cspHash is the 'sha256-...' source of an inline element's text as the
// browser's parser hands it over: line breaks normalized to \n and NUL
// replaced by U+FFFD (the router's hydration payload carries NULs in its
// route ids).
func cspHash(text []byte) string {
	s := strings.ReplaceAll(string(text), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\x00", "\uFFFD")
	sum := sha256.Sum256([]byte(s))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}
