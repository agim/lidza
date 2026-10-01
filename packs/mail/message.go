package mail

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"
)

// Attachment is a file sent with the message. Data holds the file bytes,
// encoded as base64 when stored in the JSON outbox. Name is a filename,
// not a filesystem path or URL; the pack never fetches an attachment.
// An empty ContentType is detected from Data during Send or SendTx.
type Attachment struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType,omitempty"`
	Data        []byte `json:"data"`
}

func recipientLists(msg Message) (to, cc, bcc []*mail.Address, err error) {
	to, err = mail.ParseAddressList(msg.To)
	if err != nil || len(to) == 0 {
		return nil, nil, nil, errors.New("mail: invalid To address list")
	}
	parse := func(values []string) ([]*mail.Address, error) {
		var out []*mail.Address
		for _, value := range values {
			a, err := mail.ParseAddress(value)
			if err != nil {
				return nil, errors.New("mail: invalid Cc or Bcc address")
			}
			out = append(out, a)
		}
		return out, nil
	}
	cc, err = parse(msg.Cc)
	if err != nil {
		return nil, nil, nil, err
	}
	bcc, err = parse(msg.Bcc)
	return to, cc, bcc, err
}

func addressStrings(list []*mail.Address) []string {
	out := make([]string, len(list))
	for i, address := range list {
		if address.Name == "" {
			out[i] = address.Address
		} else {
			out[i] = address.String()
		}
	}
	return out
}

func toStrings(raw string, list []*mail.Address) []string {
	if len(list) == 1 {
		return []string{raw} // Keep the existing single-recipient wire format.
	}
	return addressStrings(list)
}

func validateMessage(msg *Message, cfg Config) error {
	// Bound parsing and reject newlines before an address parser can unfold them.
	if len(msg.To) > 64<<10 || len(msg.Cc)+len(msg.Bcc) > cfg.MaxRecipients {
		return errors.New("mail: too many recipients")
	}
	for _, value := range append(append([]string{msg.To, msg.From, msg.ReplyTo}, msg.Cc...), msg.Bcc...) {
		if !safeHeader(value) {
			return errors.New("mail: invalid address header")
		}
	}
	to, cc, bcc, err := recipientLists(*msg)
	if err != nil {
		return err
	}
	if len(to)+len(cc)+len(bcc) > cfg.MaxRecipients {
		return errors.New("mail: too many recipients")
	}
	seen := make(map[string]bool, len(to)+len(cc)+len(bcc))
	for _, list := range [][]*mail.Address{to, cc, bcc} {
		for _, a := range list {
			if seen[a.Address] {
				return errors.New("mail: duplicate recipient")
			}
			seen[a.Address] = true
		}
	}
	for _, value := range []string{msg.From, msg.ReplyTo} {
		if value != "" {
			if _, err := mail.ParseAddress(value); err != nil {
				return errors.New("mail: invalid From or Reply-To address")
			}
		}
	}
	if !safeHeader(msg.Subject) || !utf8.ValidString(msg.Text) || !utf8.ValidString(msg.HTML) {
		return errors.New("mail: invalid subject or body")
	}
	if len(msg.Headers) > 100 {
		return errors.New("mail: too many headers")
	}
	for key, value := range msg.Headers {
		if key == "" || len(key) > 200 || !safeHeader(value) || len(value) > 8<<10 {
			return errors.New("mail: invalid custom header")
		}
		for _, r := range key {
			if r < '!' || r > '~' || r == ':' {
				return errors.New("mail: invalid custom header name")
			}
		}
		switch strings.ToLower(key) {
		case "to", "cc", "bcc", "from", "subject", "reply-to", "return-path", "mime-version", "content-type", "content-transfer-encoding", "content-disposition":
			return fmt.Errorf("mail: reserved header %s: use Message fields", key)
		}
	}
	if len(msg.Attachments) > cfg.MaxAttachments {
		return errors.New("mail: too many attachments")
	}
	// Clone metadata before resolving a type, so the caller's slice stays intact.
	msg.Attachments = append([]Attachment(nil), msg.Attachments...)
	remaining := cfg.MaxAttachmentBytes
	for i := range msg.Attachments {
		a := &msg.Attachments[i]
		if a.Name == "" || len(a.Name) > 255 || !safeHeader(a.Name) || strings.ContainsAny(a.Name, `/\`) || !safeHeader(a.ContentType) {
			return errors.New("mail: invalid attachment name or content type")
		}
		if len(a.Data) > remaining {
			return errors.New("mail: attachments exceed byte limit")
		}
		remaining -= len(a.Data)
		if a.ContentType == "" {
			a.ContentType = http.DetectContentType(a.Data)
		}
		kind, params, err := mime.ParseMediaType(a.ContentType)
		if err != nil || !strings.Contains(kind, "/") || strings.HasPrefix(kind, "multipart/") || len(a.ContentType) > 200 {
			return errors.New("mail: invalid attachment content type")
		}
		a.ContentType = mime.FormatMediaType(kind, params)
	}
	return nil
}

func safeHeader(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
