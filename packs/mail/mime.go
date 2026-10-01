package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"sort"
	"strings"
	"time"
)

// buildMIME nests related HTML resources inside the HTML alternative and
// regular files outside the body in multipart/mixed. Blind recipients never
// enter the headers.
func buildMIME(msg Message, from, first *mail.Address) ([]byte, string, error) {
	if msg.To == "" {
		msg.To = first.String()
	}
	to, cc, _, err := recipientLists(msg)
	if err != nil {
		return nil, "", err
	}
	id := "<" + rand.Text() + "@" + domainOf(from.Address) + ">"
	h := textproto.MIMEHeader{}
	h.Set("From", from.String())
	join := func(list []*mail.Address) string {
		values := make([]string, len(list))
		for i, address := range list {
			values[i] = address.String()
		}
		return strings.Join(values, ",\r\n ")
	}
	h.Set("To", join(to))
	if len(cc) > 0 {
		h.Set("Cc", join(cc))
	}
	h.Set("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	h.Set("Date", time.Now().Format(time.RFC1123Z))
	h.Set("Message-ID", id)
	h.Set("MIME-Version", "1.0")
	if msg.ReplyTo != "" {
		reply, err := replyAddresses(msg.ReplyTo)
		if err != nil {
			return nil, "", err
		}
		if len(reply) == 1 {
			h.Set("Reply-To", msg.ReplyTo)
		} else {
			h.Set("Reply-To", join(reply))
		}
	}
	for key, value := range msg.Headers {
		h.Set(key, value)
	}
	bodyHeader, body, err := mimeBody(msg)
	if err != nil {
		return nil, "", err
	}
	var output bytes.Buffer
	hasFiles := false
	for _, file := range msg.Attachments {
		hasFiles = hasFiles || file.ContentID == ""
	}
	if !hasFiles {
		for key, values := range bodyHeader {
			h[key] = values
		}
		if err := writeMIMEHeaders(&output, h); err != nil {
			return nil, "", err
		}
		output.Write(body)
	} else {
		mixed := multipart.NewWriter(&output)
		h.Set("Content-Type", mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mixed.Boundary()}))
		if err := writeMIMEHeaders(&output, h); err != nil {
			return nil, "", err
		}
		part, err := mixed.CreatePart(bodyHeader)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(body); err != nil {
			return nil, "", err
		}
		for _, file := range msg.Attachments {
			if file.ContentID != "" {
				continue
			}
			if err := mimeAttachment(mixed, file); err != nil {
				return nil, "", err
			}
		}
		if err := mixed.Close(); err != nil {
			return nil, "", err
		}
	}
	return output.Bytes(), id, nil
}

func mimeBody(msg Message) (textproto.MIMEHeader, []byte, error) {
	h := textproto.MIMEHeader{}
	var body bytes.Buffer
	if msg.Text != "" && msg.HTML != "" {
		alternative := multipart.NewWriter(&body)
		h.Set("Content-Type", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": alternative.Boundary()}))
		for _, content := range []struct{ kind, text string }{{"text/plain", msg.Text}, {"text/html", msg.HTML}} {
			header, data, err := mimeContent(msg, content.kind, content.text)
			if err != nil {
				return nil, nil, err
			}
			part, err := alternative.CreatePart(header)
			if err != nil {
				return nil, nil, err
			}
			if _, err := part.Write(data); err != nil {
				return nil, nil, err
			}
		}
		if err := alternative.Close(); err != nil {
			return nil, nil, err
		}
	} else {
		kind, text := "text/plain", msg.Text
		if msg.HTML != "" {
			kind, text = "text/html", msg.HTML
		}
		return mimeContent(msg, kind, text)
	}
	return h, body.Bytes(), nil
}

func mimeContent(msg Message, kind, text string) (textproto.MIMEHeader, []byte, error) {
	h := textproto.MIMEHeader{"Content-Type": {kind + "; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}}
	var body bytes.Buffer
	if err := writeQuotedPrintable(&body, text); err != nil {
		return nil, nil, err
	}
	var inline []Attachment
	if kind == "text/html" {
		for _, file := range msg.Attachments {
			if file.ContentID != "" {
				inline = append(inline, file)
			}
		}
	}
	if len(inline) == 0 {
		return h, body.Bytes(), nil
	}
	var resources bytes.Buffer
	related := multipart.NewWriter(&resources)
	part, err := related.CreatePart(h)
	if err != nil {
		return nil, nil, err
	}
	if _, err := part.Write(body.Bytes()); err != nil {
		return nil, nil, err
	}
	for _, file := range inline {
		if err := mimeAttachment(related, file); err != nil {
			return nil, nil, err
		}
	}
	if err := related.Close(); err != nil {
		return nil, nil, err
	}
	return textproto.MIMEHeader{"Content-Type": {mime.FormatMediaType("multipart/related", map[string]string{"boundary": related.Boundary(), "type": "text/html"})}}, resources.Bytes(), nil
}

func mimeAttachment(writer *multipart.Writer, file Attachment) error {
	disposition := "attachment"
	h := textproto.MIMEHeader{"Content-Type": {file.ContentType}, "Content-Transfer-Encoding": {"base64"}}
	if file.ContentID != "" {
		disposition = "inline"
		h.Set("Content-ID", "<"+file.ContentID+">")
	}
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": file.Name}))
	part, err := writer.CreatePart(h)
	if err != nil {
		return err
	}
	return writeBase64(part, file.Data)
}

func writeMIMEHeaders(w io.Writer, headers textproto.MIMEHeader) error {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range headers[key] {
			if _, err := fmt.Fprintf(w, "%s: %s\r\n", key, value); err != nil {
				return err
			}
		}
	}
	_, err := io.WriteString(w, "\r\n")
	return err
}

func writeQuotedPrintable(w io.Writer, text string) error {
	qp := newQPWriter(w)
	if _, err := io.WriteString(qp, text); err != nil {
		return err
	}
	return qp.Close()
}

type base64Lines struct {
	w      io.Writer
	column int
}

func (w *base64Lines) Write(data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		n := min(76-w.column, len(data))
		actual, err := w.w.Write(data[:n])
		written += actual
		w.column += actual
		if err != nil {
			return written, err
		}
		if actual != n {
			return written, io.ErrShortWrite
		}
		data = data[n:]
		if w.column == 76 {
			if _, err := io.WriteString(w.w, "\r\n"); err != nil {
				return written, err
			}
			w.column = 0
		}
	}
	return written, nil
}

func writeBase64(w io.Writer, data []byte) error {
	lines := &base64Lines{w: w}
	encoder := base64.NewEncoder(base64.StdEncoding, lines)
	if _, err := encoder.Write(data); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	if lines.column > 0 {
		_, err := io.WriteString(w, "\r\n")
		return err
	}
	return nil
}
