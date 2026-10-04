package authcore

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/scrypt"
)

// Legacy memory-hard verification is serialized and bounded to 128 MiB per
// operation. New credentials always use the current PBKDF2-SHA256 writer.
var legacyMemorySlots = make(chan struct{}, 1)

func legacyVerify(ctx context.Context, password, encoded string) (bool, error) {
	if len(password) > 1024 || len(encoded) > 2048 {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	select {
	case passwordSlots <- struct{}{}:
		defer func() { <-passwordSlots }()
	case <-ctx.Done():
		return false, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	parts := strings.Split(encoded, "$")
	if len(parts) > 0 && parts[0] == "pbkdf2_sha1" {
		if len(parts) != 4 || parts[2] == "" || len(parts[2]) > 128 {
			return false, nil
		}
		iterations, err := strconv.Atoi(parts[1])
		if err != nil || iterations < 1 || iterations > 2000000 {
			return false, nil
		}
		expected, err := base64.StdEncoding.DecodeString(parts[3])
		if err != nil || len(expected) != 20 {
			return false, nil
		}
		actual, err := pbkdf2.Key(sha1.New, password, []byte(parts[2]), iterations, 20)
		return err == nil && subtle.ConstantTimeCompare(actual, expected) == 1, err
	}
	if strings.HasPrefix(encoded, "bcrypt_sha256$") || strings.HasPrefix(encoded, "bcrypt$") {
		hash := encoded[strings.IndexByte(encoded, '$')+1:]
		cost, err := bcrypt.Cost([]byte(hash))
		if err != nil || cost < 4 || cost > 14 {
			return false, nil
		}
		input := []byte(password)
		if parts[0] == "bcrypt_sha256" {
			sum := sha256.Sum256(input)
			input = []byte(hex.EncodeToString(sum[:]))
		}
		return bcrypt.CompareHashAndPassword([]byte(hash), input) == nil, nil
	}
	select {
	case legacyMemorySlots <- struct{}{}:
		defer func() { <-legacyMemorySlots }()
	case <-ctx.Done():
		return false, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(parts) == 6 && parts[0] == "scrypt" {
		n, e1 := strconv.Atoi(parts[1])
		r, e2 := strconv.Atoi(parts[3])
		p, e3 := strconv.Atoi(parts[4])
		if e1 != nil || e2 != nil || e3 != nil || n < 2 || n > 1<<20 || n&(n-1) != 0 || r < 1 || r > 32 || p < 1 || p > 16 || int64(n)*int64(r) > 1048576 || int64(n)*int64(r)*int64(p) > 8388608 || parts[2] == "" || len(parts[2]) > 128 {
			return false, nil
		}
		expected, err := base64.StdEncoding.DecodeString(parts[5])
		if err != nil || len(expected) != 64 {
			return false, nil
		}
		actual, err := scrypt.Key([]byte(password), []byte(parts[2]), n, r, p, 64)
		return err == nil && subtle.ConstantTimeCompare(actual, expected) == 1, err
	}
	if len(parts) == 6 && parts[0] == "argon2" && (parts[1] == "argon2id" || parts[1] == "argon2i") && parts[2] == "v=19" {
		var memory, iterations, threads uint32
		if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil || memory < 8 || memory > 131072 || iterations < 1 || iterations > 8 || threads < 1 || threads > 16 || memory < 8*threads {
			return false, nil
		}
		salt, err := base64.RawStdEncoding.DecodeString(parts[4])
		if err != nil || len(salt) < 8 || len(salt) > 128 {
			return false, nil
		}
		expected, err := base64.RawStdEncoding.DecodeString(parts[5])
		if err != nil || len(expected) < 16 || len(expected) > 64 {
			return false, nil
		}
		var actual []byte
		if parts[1] == "argon2id" {
			actual = argon2.IDKey([]byte(password), salt, iterations, memory, uint8(threads), uint32(len(expected)))
		} else {
			actual = argon2.Key([]byte(password), salt, iterations, memory, uint8(threads), uint32(len(expected)))
		}
		return subtle.ConstantTimeCompare(actual, expected) == 1, nil
	}
	return false, nil
}
