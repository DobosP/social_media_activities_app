package platform

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// CursorCodec preserves Django TimestampSigner's ops.api-cursor-v1 contract.
// The signing key is supplied at startup and never appears in URLs or logs.
type CursorCodec struct {
	Key []byte
	Now func() time.Time
}

const base62Digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func base62(n int64) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append(out, base62Digits[n%62])
		n /= 62
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}
func (c CursorCodec) signature(value string) string {
	derived := sha256.Sum256(append([]byte("ops.api-cursor-v1signer"), c.Key...))
	mac := hmac.New(sha256.New, derived[:])
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (c CursorCodec) Encode(offset int) string {
	if len(c.Key) < 32 {
		return ""
	}
	if offset < 0 {
		offset = 0
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	raw := `{"offset":` + strconv.Itoa(offset) + `}`
	value := base64.RawURLEncoding.EncodeToString([]byte(raw)) + ":" + base62(now().Unix())
	return value + ":" + c.signature(value)
}
func (c CursorCodec) Decode(value string) int {
	if value == "" || len(value) > 4096 || len(c.Key) < 32 {
		return 0
	}
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0
	}
	signed := parts[0] + ":" + parts[1]
	if !hmac.Equal([]byte(c.signature(signed)), []byte(parts[2])) {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0
	}
	var payload struct {
		Offset json.Number `json:"offset"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return 0
	}
	offset, err := strconv.ParseInt(payload.Offset.String(), 10, 64)
	if err != nil || offset < 0 {
		return 0
	}
	return int(offset)
}
func ParseLimit(raw string, defaultLimit, maxLimit int) int {
	limit, err := strconv.Atoi(raw)
	if err != nil {
		limit = defaultLimit
	}
	if limit < 1 {
		return 1
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}
