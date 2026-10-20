package web

import (
	"bytes"
	"errors"
	"regexp"
)

// The compatibility base already renders this notice. Cached native pages do
// not inherit that base, so the shared response boundary preserves the same
// producer-selected development disclosure for every full-page renderer.
const localPOCNotice = `<p class="banner local-poc-banner" role="status"><strong>LOCAL POC — not cleared for publication</strong></p>`

var localPOCMain = regexp.MustCompile(`<main(?:\s[^>]*)?>`)

func preserveLocalPOCNotice(body []byte, selected bool) ([]byte, error) {
	if !selected {
		return body, nil
	}
	notice := []byte(localPOCNotice)
	if count := bytes.Count(body, notice); count > 1 {
		return nil, errors.New("duplicate local POC disclosure")
	} else if count == 1 {
		return body, nil
	}
	main := localPOCMain.FindIndex(body)
	if main == nil {
		// Routed fragments are displayed within their already-labelled page.
		return body, nil
	}
	result := make([]byte, 0, len(body)+len(notice))
	result = append(result, body[:main[1]]...)
	result = append(result, notice...)
	result = append(result, body[main[1]:]...)
	return result, nil
}
