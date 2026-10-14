package googleworkspace

import (
	"encoding/base64"
	"io"
	"mime"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding/htmlindex"
)

// Limits on what one message is parsed into. A message beyond them is cut and
// says so rather than being refused.
const (
	maxBodyBytes  = 2 << 20
	maxMIMEDepth  = 20
	maxMIMEParts  = 500
	maxHeaderRune = 1000
)

// gmailPart is one MIME part of a Gmail API message resource.
type gmailPart struct {
	PartID   string        `json:"partId"`
	MimeType string        `json:"mimeType"`
	Filename string        `json:"filename"`
	Headers  []gmailHeader `json:"headers"`
	Body     struct {
		AttachmentID string `json:"attachmentId"`
		Size         int64  `json:"size"`
		Data         string `json:"data"`
	} `json:"body"`
	Parts []gmailPart `json:"parts"`
}

type gmailHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// gmailMessage is the part of a Gmail API message resource Neo Box reads.
type gmailMessage struct {
	ID           string    `json:"id"`
	ThreadID     string    `json:"threadId"`
	LabelIDs     []string  `json:"labelIds"`
	Snippet      string    `json:"snippet"`
	InternalDate string    `json:"internalDate"`
	Payload      gmailPart `json:"payload"`
}

// MailAttachment is one attachment part of a message. PartID names it for a
// download: Gmail's attachment IDs are not stable across reads, so a download
// reads the message again and finds the part by PartID.
type MailAttachment struct {
	PartID   string
	Filename string
	MimeType string
	Size     int64
	// Inline parts (such as images the HTML refers to) are listed but not
	// rendered.
	Inline bool
	// Downloadable is false when Gmail gave the part no attachment ID.
	Downloadable bool

	attachmentID string
}

// parsedMessage is the readable content of a message.
type parsedMessage struct {
	plain, html   []string
	attachments   []MailAttachment
	plainTruncate bool
	htmlTruncate  bool
}

func headerValue(headers []gmailHeader, name string) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

var wordDecoder = &mime.WordDecoder{CharsetReader: func(label string, input io.Reader) (io.Reader, error) {
	return charset.NewReaderLabel(label, input)
}}

// decodeHeader decodes RFC 2047 words and drops anything but printable text.
func decodeHeader(v string) string {
	if d, err := wordDecoder.DecodeHeader(v); err == nil {
		v = d
	}
	v = strings.ToValidUTF8(v, "\uFFFD")
	v = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\r' || r == '\n' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
	v = strings.Join(strings.Fields(v), " ")
	if utf8.RuneCountInString(v) > maxHeaderRune {
		v = string([]rune(v)[:maxHeaderRune])
	}
	return v
}

// messageDate prefers the Date header and falls back to Gmail's internal date.
func messageDate(headers []gmailHeader, internal string) time.Time {
	if raw := headerValue(headers, "Date"); raw != "" {
		if t, err := mail.ParseDate(raw); err == nil {
			return t.UTC()
		}
	}
	if ms, err := strconv.ParseInt(internal, 10, 64); err == nil && ms > 0 {
		return time.UnixMilli(ms).UTC()
	}
	return time.Time{}
}

// parsePayload walks the MIME tree: text/plain and text/html leaves become the
// body, every other leaf with a file name or an attachment ID is an attachment.
func parsePayload(root *gmailPart) *parsedMessage {
	p := &parsedMessage{}
	count := 0
	var walk func(part *gmailPart, depth int)
	walk = func(part *gmailPart, depth int) {
		count++
		if count > maxMIMEParts || depth > maxMIMEDepth {
			return
		}
		mediaType, params, _ := mime.ParseMediaType(headerValue(part.Headers, "Content-Type"))
		if mediaType == "" {
			mediaType = strings.ToLower(strings.TrimSpace(part.MimeType))
		}
		disposition, _, _ := mime.ParseMediaType(headerValue(part.Headers, "Content-Disposition"))
		if len(part.Parts) > 0 && part.Filename == "" {
			for i := range part.Parts {
				walk(&part.Parts[i], depth+1)
			}
			return
		}
		isText := mediaType == "text/plain" || mediaType == "text/html"
		if isText && part.Filename == "" && !strings.EqualFold(disposition, "attachment") && part.Body.AttachmentID == "" {
			text, truncated := decodeText(part.Body.Data, params["charset"])
			if text == "" {
				return
			}
			if mediaType == "text/plain" {
				p.plain = append(p.plain, text)
				p.plainTruncate = p.plainTruncate || truncated
			} else {
				p.html = append(p.html, text)
				p.htmlTruncate = p.htmlTruncate || truncated
			}
			return
		}
		if part.Filename == "" && part.Body.AttachmentID == "" {
			return
		}
		mt := mediaType
		if mt == "" {
			mt = "application/octet-stream"
		}
		p.attachments = append(p.attachments, MailAttachment{
			PartID:       part.PartID,
			Filename:     decodeHeader(part.Filename),
			MimeType:     mt,
			Size:         part.Body.Size,
			Inline:       strings.EqualFold(disposition, "inline") || headerValue(part.Headers, "Content-ID") != "",
			Downloadable: part.Body.AttachmentID != "",
			attachmentID: part.Body.AttachmentID,
		})
	}
	walk(root, 0)
	return p
}

// decodeBase64URL accepts Gmail's base64url with or without padding.
func decodeBase64URL(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	return base64.RawURLEncoding.DecodeString(s)
}

// decodeText turns a part's base64url body into UTF-8 using the part's
// declared charset; an unknown charset is read as UTF-8 with replacements.
func decodeText(data, charsetName string) (string, bool) {
	if data == "" {
		return "", false
	}
	raw, err := decodeBase64URL(data)
	if err != nil {
		return "", false
	}
	truncated := false
	if len(raw) > maxBodyBytes {
		raw, truncated = raw[:maxBodyBytes], true
	}
	if charsetName != "" && !strings.EqualFold(charsetName, "utf-8") {
		if enc, err := htmlindex.Get(charsetName); err == nil {
			if out, err := enc.NewDecoder().Bytes(raw); err == nil {
				raw = out
			}
		}
	}
	return strings.ToValidUTF8(string(raw), "\uFFFD"), truncated
}

// htmlToText is the plain-text fallback for a message that only has HTML. It
// keeps text and line breaks and drops everything else; it is for reading, not
// a sanitizer.
func htmlToText(src string) string {
	z := html.NewTokenizer(strings.NewReader(src))
	var b strings.Builder
	skip := 0
	newline := func() {
		s := b.String()
		if s != "" && !strings.HasSuffix(s, "\n") {
			b.WriteByte('\n')
		}
	}
	for {
		switch z.Next() {
		case html.ErrorToken:
			return collapseBlank(b.String())
		case html.TextToken:
			if skip == 0 {
				b.WriteString(string(z.Text()))
			}
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			tt := z.Token()
			switch tt.Data {
			case "script", "style", "head", "title":
				if tt.Type == html.StartTagToken {
					skip++
				} else if tt.Type == html.EndTagToken && skip > 0 {
					skip--
				}
			case "br", "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "table", "ul", "ol":
				if skip == 0 {
					newline()
				}
			}
		}
	}
}

// collapseBlank trims each line and keeps at most one blank line in a row.
func collapseBlank(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r", ""), "\n")
	var out []string
	blank := false
	for _, l := range lines {
		l = strings.Join(strings.Fields(l), " ")
		if l == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		blank = false
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// hasAttachmentHint reports whether a metadata-only message looks like it
// carries an attachment. Gmail's metadata format may omit parts, so a
// multipart/mixed container counts too; it is a hint, not a guarantee.
func hasAttachmentHint(root *gmailPart) bool {
	var walk func(p *gmailPart, depth int) bool
	walk = func(p *gmailPart, depth int) bool {
		if depth > maxMIMEDepth {
			return false
		}
		if p.Filename != "" {
			return true
		}
		for i := range p.Parts {
			if walk(&p.Parts[i], depth+1) {
				return true
			}
		}
		return false
	}
	if walk(root, 0) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(strings.SplitN(root.MimeType, ";", 2)[0]), "multipart/mixed")
}
