package storage

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const multipartPartSize = 16 << 20
const multipartMaxParts = 10_000

// PutMultipart streams an object of exactly size bytes to an S3-compatible
// backend, buffering one 16 MiB part. The configured MaxSize and Timeout
// apply. Use Put for small objects and other providers.
func (s *Storage) PutMultipart(ctx context.Context, key string, r io.Reader, size int64, opt PutOptions) (Object, error) {
	if err := checkKey(key); err != nil {
		return Object{}, err
	}
	v := s.now()
	provider, ok := v.p.(*s3)
	if !ok {
		return Object{}, errors.New("storage: multipart upload requires an S3-compatible provider")
	}
	if size <= 0 || size > v.cfg.MaxSize || size > int64(multipartPartSize)*multipartMaxParts {
		return Object{}, errors.New("storage: multipart size exceeds configured or supported limits")
	}
	ctx, cancel := context.WithTimeout(ctx, v.cfg.Timeout)
	defer cancel()
	object, err := provider.putMultipart(ctx, v.full(key), r, size, multipartPartSize, opt)
	return v.short(object), err
}

type multipartPart struct {
	Number int    `xml:"PartNumber"`
	ETag   string `xml:"ETag"`
}

func (p *s3) putMultipart(ctx context.Context, key string, r io.Reader, size int64, partSize int, opt PutOptions) (_ Object, resultErr error) {
	readPart := func(part []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := io.ReadFull(r, part)
		return err
	}
	buffer := make([]byte, min(int64(partSize), size))
	if err := readPart(buffer); err != nil {
		return Object{}, fmt.Errorf("storage: read first upload part: %w", err)
	}
	if opt.ContentType == "" {
		opt.ContentType = detectContentType(key, buffer)
	}
	u := p.objectURL(key)
	u.RawQuery = "uploads="
	response, err := p.do(ctx, http.MethodPost, u, nil, map[string]string{"Content-Type": opt.ContentType, "Cache-Control": opt.CacheControl})
	if err != nil {
		return Object{}, err
	}
	var init struct {
		XMLName xml.Name `xml:"InitiateMultipartUploadResult"`
		ID      string   `xml:"UploadId"`
	}
	err = readMultipartResponse(response, &init)
	if err != nil || init.ID == "" {
		return Object{}, errors.New("storage: multipart initiation did not return an upload ID")
	}
	query := url.Values{"uploadId": {init.ID}}
	defer func() {
		if resultErr == nil {
			return
		}
		abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		u.RawQuery = query.Encode()
		res, err := p.do(abortCtx, http.MethodDelete, u, nil, nil)
		if err == nil {
			res.Body.Close()
		} else if !errors.Is(err, ErrNotFound) {
			resultErr = errors.Join(resultErr, fmt.Errorf("storage: abort incomplete multipart upload: %w", err))
		}
	}()

	request := struct {
		XMLName xml.Name        `xml:"CompleteMultipartUpload"`
		Parts   []multipartPart `xml:"Part"`
	}{}
	for number, remaining := 1, size; remaining > 0; number++ {
		part := buffer[:min(remaining, int64(partSize))]
		if number > 1 {
			if err := readPart(part); err != nil {
				return Object{}, fmt.Errorf("storage: read upload part %d: %w", number, err)
			}
		}
		u.RawQuery = url.Values{"uploadId": {init.ID}, "partNumber": {strconv.Itoa(number)}}.Encode()
		res, err := p.do(ctx, http.MethodPut, u, part, nil)
		if err != nil {
			return Object{}, err
		}
		tag := res.Header.Get("ETag")
		res.Body.Close()
		if tag == "" {
			return Object{}, errors.New("storage: upload part did not return an ETag")
		}
		request.Parts = append(request.Parts, multipartPart{Number: number, ETag: tag})
		remaining -= int64(len(part))
	}
	var extra [1]byte
	if err := readPart(extra[:]); err == nil {
		return Object{}, errors.New("storage: multipart reader exceeded declared size")
	} else if !errors.Is(err, io.EOF) {
		return Object{}, fmt.Errorf("storage: check multipart reader size: %w", err)
	}
	body, err := xml.Marshal(request)
	if err != nil {
		return Object{}, err
	}
	u.RawQuery = query.Encode()
	response, err = p.do(ctx, http.MethodPost, u, body, map[string]string{"Content-Type": "application/xml"})
	if err != nil {
		return Object{}, err
	}
	var complete struct {
		XMLName xml.Name `xml:"CompleteMultipartUploadResult"`
		ETag    string   `xml:"ETag"`
	}
	// S3 can send HTTP 200 followed by an XML error instead of a result.
	if err := readMultipartResponse(response, &complete); err != nil {
		return Object{}, fmt.Errorf("storage: multipart completion was not confirmed: %w", err)
	}
	if complete.ETag == "" {
		return Object{}, errors.New("storage: multipart completion did not return an ETag")
	}
	return Object{Key: key, Size: size, ContentType: opt.ContentType, ModifiedAt: p.now().UTC(), ETag: strings.Trim(complete.ETag, `"`)}, nil
}

func readMultipartResponse(response *http.Response, target any) error {
	defer response.Body.Close()
	return xml.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(target)
}
