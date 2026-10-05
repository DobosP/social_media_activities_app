package jobs

import (
	"errors"
	"strconv"
	"strings"
)

// Read permits at most 10000 pages, each declaring at most 1000000 withheld
// records. Keep the aggregate in int64 without changing the producer gates.
const maxProductRefusalWithheld int64 = 10_000_000_000

const (
	refusalSchemaNotReady uint8 = 1 << iota
	refusalPolicyDrift
	refusalUnknownCondition
)

// ProductRefusalError preserves only numeric counts and fixed public condition
// codes. Arbitrary producer notes are never stored on an escaping error.
type ProductRefusalError struct {
	withheld   int64
	conditions uint8
}

func (e *ProductRefusalError) Unwrap() error   { return ErrProductRefused }
func (e *ProductRefusalError) Withheld() int64 { return e.withheld }
func (e *ProductRefusalError) Reasons() []string {
	out := []string{}
	if e.conditions&refusalSchemaNotReady != 0 {
		out = append(out, "schema not ready")
	}
	if e.conditions&refusalPolicyDrift != 0 {
		out = append(out, "policy ruleset drifted")
	}
	if e.conditions&refusalUnknownCondition != 0 {
		out = append(out, "producer reported unavailable")
	}
	return out
}
func (e *ProductRefusalError) Error() string {
	message := ErrProductRefused.Error()
	if e.withheld > 0 {
		message += ": withheld " + strconv.FormatInt(e.withheld, 10) + " item(s)"
	}
	if reasons := e.Reasons(); len(reasons) > 0 {
		message += ": " + strings.Join(reasons, "; ")
	}
	return message
}

func publicRefusalCondition(note string) uint8 {
	switch note {
	case "schema not ready":
		return refusalSchemaNotReady
	case "policy ruleset drifted":
		return refusalPolicyDrift
	default:
		return refusalUnknownCondition
	}
}

func productRefusal(withheld int64, conditions uint8) error {
	if withheld < 0 || withheld > maxProductRefusalWithheld {
		return errors.New("invalid RO-EDU refusal count")
	}
	return &ProductRefusalError{withheld: withheld, conditions: conditions & (refusalSchemaNotReady | refusalPolicyDrift | refusalUnknownCondition)}
}
