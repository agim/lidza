package mail

import (
	"io"
	"mime/quotedprintable"
)

func newQPWriter(w io.Writer) *quotedprintable.Writer { return quotedprintable.NewWriter(w) }
