package ratelimit

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

// JSONFieldKey builds a KeyFunc that buckets on one string field of a JSON
// body, reading at most maxBytes and restoring the body for the handler. It
// decodes the first JSON value as the handler does, so trailing bytes cannot
// hide the field. A body over maxBytes, or a value longer than maxRunes, yields
// no key: the route's body limit and its schema refuse such a request before
// any handler runs. A body whose read fails yields no key either, and the
// restored body fails the same way for the next reader: a request body reports
// a truncation once and a clean end afterwards, so without that the reader
// behind the limiter would take a body cut short for a whole one.
func JSONFieldKey(field string, maxBytes int64, maxRunes int) KeyFunc {
	return JSONFieldKeyFunc(field, maxBytes, maxRunes, nil)
}

// JSONFieldKeyFunc is JSONFieldKey with a caller-supplied canonicaliser.
func JSONFieldKeyFunc(field string, maxBytes int64, maxRunes int, canonical func(string) string) KeyFunc {
	return func(r *http.Request) string {
		if r.Body == nil {
			return ""
		}

		buf, err := peekBody(r, maxBytes+1)
		if err != nil || int64(len(buf)) > maxBytes {
			return ""
		}

		var fields map[string]json.RawMessage
		if err := json.NewDecoder(bytes.NewReader(buf)).Decode(&fields); err != nil {
			return ""
		}
		raw, ok := fields[field]
		if !ok {
			return ""
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || utf8.RuneCountInString(value) > maxRunes {
			return ""
		}
		if canonical != nil {
			return canonical(value)
		}
		return strings.ToLower(strings.TrimSpace(value))
	}
}

// peekBody reads up to limit bytes of r's body and puts the body back for the
// next reader: the bytes read, then the rest, or the error that ended the read.
func peekBody(r *http.Request, limit int64) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r.Body, limit))
	rest := io.Reader(r.Body)
	if err != nil {
		rest = failedRead{err}
	}
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(buf), rest), r.Body}
	return buf, err
}

// failedRead stands where a body stopped being readable and answers every
// read with the error that stopped it.
type failedRead struct{ err error }

func (f failedRead) Read([]byte) (int, error) { return 0, f.err }
