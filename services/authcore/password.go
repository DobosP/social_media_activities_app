package authcore

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const PasswordIterations = 1_000_000

// Hashing is CPU intensive. Bound it across all Service instances in a process.
var passwordSlots = make(chan struct{}, 4)

func passwordKey(ctx context.Context, password, salt string, iterations int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case passwordSlots <- struct{}{}:
		defer func() { <-passwordSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return pbkdf2.Key(sha256.New, password, []byte(salt), iterations, 32)
}

// HashPassword creates a Django-compatible PBKDF2-SHA256 password hash.
func HashPassword(ctx context.Context, password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", errors.New("password must contain between 12 and 1024 bytes")
	}
	salt := rand.Text()
	key, err := passwordKey(ctx, password, salt, PasswordIterations)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", PasswordIterations, salt, base64.StdEncoding.EncodeToString(key)), nil
}

// VerifyPassword also accepts existing Django hashes. Unsupported or unreasonable
// work factors fail closed instead of allowing an attacker to select CPU work.
func VerifyPassword(ctx context.Context, password, encoded string) (bool, error) {
	if !strings.HasPrefix(encoded, "pbkdf2_sha256$") {
		return legacyVerify(ctx, password, encoded)
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" || len(password) > 1024 || len(parts[2]) > 128 || parts[2] == "" {
		return false, nil
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 || iterations > 2_000_000 {
		return false, nil
	}
	expected, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil || len(expected) != 32 {
		return false, nil
	}
	key, err := passwordKey(ctx, password, parts[2], iterations)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(key, expected) == 1, nil
}
