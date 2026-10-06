package lidza

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// SecurityTxtPath is where the app says how to report a vulnerability
// (RFC 9116), served when SECURITY_CONTACT is set.
const SecurityTxtPath = "/.well-known/security.txt"

// securityTxt serves SecurityTxtPath from contact, SECURITY_CONTACT: one
// or more addresses, comma-separated, each an email (sent as mailto:),
// or a mailto:, tel: or https:// URL. policy, SECURITY_POLICY, is an
// optional https:// page on how reports are handled. Expires is always
// half a year ahead, so the file never goes stale; Canonical is the
// file's own address when appURL is https://. Without a contact the
// path is next's (an app's own file in its build).
func securityTxt(contact, policy, appURL string, next http.Handler) (http.Handler, error) {
	if strings.TrimSpace(contact) == "" {
		return next, nil
	}
	var lines []string
	for _, c := range strings.Split(contact, ",") {
		c = strings.TrimSpace(c)
		switch {
		case c == "":
			continue
		case strings.HasPrefix(c, "mailto:"), strings.HasPrefix(c, "tel:"), strings.HasPrefix(c, "https://"):
		case strings.Contains(c, "@") && !strings.Contains(c, ":"):
			c = "mailto:" + c
		default:
			return nil, fmt.Errorf("SECURITY_CONTACT: %q: an email, or a mailto:, tel: or https:// URL", c)
		}
		lines = append(lines, "Contact: "+c)
	}
	if policy = strings.TrimSpace(policy); policy != "" {
		if !strings.HasPrefix(policy, "https://") {
			return nil, fmt.Errorf("SECURITY_POLICY: %q: an https:// URL", policy)
		}
		lines = append(lines, "Policy: "+policy)
	}
	if strings.HasPrefix(appURL, "https://") {
		lines = append(lines, "Canonical: "+strings.TrimRight(appURL, "/")+SecurityTxtPath)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != SecurityTxtPath {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		expires := time.Now().UTC().AddDate(0, 6, 0).Truncate(24 * time.Hour)
		body := strings.Join(lines, "\n") + "\nExpires: " + expires.Format(time.RFC3339) + "\n"
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write([]byte(body))
	}), nil
}
