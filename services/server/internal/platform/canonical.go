package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// pythonFloat follows CPython's shortest-roundtrip decimal representation and
// exponent cutoffs. JSONB may expand exponents, but retains the numeric scale.
func pythonFloat(value float64) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "", errors.New("nonfinite audit number")
	}
	scientific := strconv.FormatFloat(value, 'e', -1, 64)
	at := strings.LastIndexByte(scientific, 'e')
	exponent, err := strconv.Atoi(scientific[at+1:])
	if err != nil {
		return "", err
	}
	if exponent < -4 || exponent >= 16 {
		return scientific, nil
	}
	fixed := strconv.FormatFloat(value, 'f', -1, 64)
	if !strings.Contains(fixed, ".") {
		fixed += ".0"
	}
	return fixed, nil
}
func asciiString(b *strings.Builder, value string) {
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 32 || r >= 127 {
				if r <= 0xffff {
					fmt.Fprintf(b, `\u%04x`, r)
				} else {
					a, c := utf16.EncodeRune(r)
					fmt.Fprintf(b, `\u%04x\u%04x`, a, c)
				}
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

func writeCanonical(b *strings.Builder, v reflect.Value, storage bool) error {
	if !v.IsValid() {
		b.WriteString("null")
		return nil
	}
	if v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			b.WriteString("null")
			return nil
		}
		return writeCanonical(b, v.Elem(), storage)
	}
	if v.Type() == reflect.TypeFor[json.Number]() {
		number := v.Interface().(json.Number)
		raw := number.String()
		if strings.ContainsAny(raw, ".eE") {
			value, err := number.Float64()
			if err != nil {
				return err
			}
			rendered, err := pythonFloat(value)
			if err != nil {
				return err
			}
			if storage {
				rendered = strconv.FormatFloat(value, 'f', -1, 64)
				if !strings.Contains(rendered, ".") {
					rendered += ".0"
				}
			}
			b.WriteString(rendered)
			return nil
		}
		digits := strings.TrimPrefix(raw, "-")
		if digits == "" || (len(digits) > 1 && digits[0] == '0') {
			return errors.New("invalid audit integer")
		}
		for _, r := range digits {
			if r < '0' || r > '9' {
				return errors.New("invalid audit integer")
			}
		}
		b.WriteString(raw)
		return nil
	}
	if v.CanInterface() {
		if marshaler, ok := v.Interface().(json.Marshaler); ok {
			raw, err := marshaler.MarshalJSON()
			if err != nil {
				return err
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			var decoded any
			if err = decoder.Decode(&decoded); err != nil {
				return err
			}
			return writeCanonical(b, reflect.ValueOf(decoded), storage)
		}
	}
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case reflect.String:
		asciiString(b, v.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		b.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		b.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		raw, err := pythonFloat(v.Float())
		if err != nil {
			return err
		}
		if storage {
			raw = strconv.FormatFloat(v.Float(), 'f', -1, 64)
			if !strings.Contains(raw, ".") {
				raw += ".0"
			}
		}
		b.WriteString(raw)
	case reflect.Map:
		if v.IsNil() {
			b.WriteString("null")
			return nil
		}
		if v.Type().Key().Kind() != reflect.String {
			return errors.New("audit object keys must be strings")
		}
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		b.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			asciiString(b, key.String())
			b.WriteByte(':')
			if err := writeCanonical(b, v.MapIndex(key), storage); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			b.WriteString("null")
			return nil
		}
		b.WriteByte('[')
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, v.Index(i), storage); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	default:
		raw, err := json.Marshal(v.Interface())
		if err != nil {
			return err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err = decoder.Decode(&value); err != nil {
			return err
		}
		return writeCanonical(b, reflect.ValueOf(value), storage)
	}
	return nil
}

// CanonicalJSON matches Python json.dumps(sort_keys=True,separators=(",",":"))
// including numeric type, shortest floats and ensure_ascii string escaping.
func CanonicalJSON(value any) ([]byte, error) {
	var b strings.Builder
	if err := writeCanonical(&b, reflect.ValueOf(value), false); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// StoredAuditJSON preserves floating-point scale through PostgreSQL JSONB.
func StoredAuditJSON(value any) ([]byte, error) {
	var b strings.Builder
	if err := writeCanonical(&b, reflect.ValueOf(value), true); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}
