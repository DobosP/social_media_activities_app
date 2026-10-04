package platform

import (
	"bytes"
	"compress/zlib"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

func (c CursorCodec) saltedSignature(salt, value string) string {
	key := sha256.Sum256(append([]byte(salt+"signer"), c.Key...))
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// SignJSON preserves supplied struct field order, matching Django JSONSerializer
// insertion order. Maps are unsuitable when byte-identical key order is needed.
func (c CursorCodec) SignJSON(salt string, payload any) string {
	if len(c.Key) < 32 || len(salt) > 128 {
		return ""
	}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(payload) != nil {
		return ""
	}
	var ascii strings.Builder
	for _, r := range strings.TrimSuffix(raw.String(), "\n") {
		if r < 128 {
			ascii.WriteRune(r)
		} else if r < 0x10000 {
			fmt.Fprintf(&ascii, `\u%04x`, r)
		} else {
			n := r - 0x10000
			fmt.Fprintf(&ascii, `\u%04x\u%04x`, 0xd800+n/1024, 0xdc00+n%1024)
		}
	}
	if ascii.Len() > 16384 {
		return ""
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	value := base64.RawURLEncoding.EncodeToString([]byte(ascii.String())) + ":" + base62(now().Unix())
	return value + ":" + c.saltedSignature(salt, value)
}
func (c CursorCodec) UnsignJSON(salt, token string, payload any, maxAge time.Duration) error {
	if token == "" || len(token) > 24000 || len(salt) > 128 || len(c.Key) < 32 {
		return ErrInvalid
	}
	parts := strings.Split(token, ":")
	if len(parts) != 3 {
		return ErrInvalid
	}
	signed := parts[0] + ":" + parts[1]
	if !hmac.Equal([]byte(c.saltedSignature(salt, signed)), []byte(parts[2])) {
		return ErrInvalid
	}
	seconds := int64(0)
	if len(parts[1]) == 0 || len(parts[1]) > 11 {
		return ErrInvalid
	}
	for _, digit := range parts[1] {
		n := strings.IndexRune(base62Digits, digit)
		if n < 0 || seconds > (1<<63-1-int64(n))/62 {
			return ErrInvalid
		}
		seconds = seconds*62 + int64(n)
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	if maxAge > 0 && now().Sub(time.Unix(seconds, 0)) > maxAge {
		return ErrInvalid
	}
	encoded := parts[0]
	compressed := strings.HasPrefix(encoded, ".")
	if compressed {
		encoded = encoded[1:]
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) > 16384 {
		return ErrInvalid
	}
	if compressed {
		reader, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return ErrInvalid
		}
		raw, err = io.ReadAll(io.LimitReader(reader, 16385))
		_ = reader.Close()
		if err != nil || len(raw) > 16384 {
			return ErrInvalid
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(payload) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
