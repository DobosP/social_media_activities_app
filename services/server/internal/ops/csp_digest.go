package ops

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

type CSPGroup struct {
	Count     int    `json:"count"`
	Directive string `json:"directive"`
	Blocked   string `json:"blocked"`
	Document  string `json:"document"`
}
type CSPDigest struct {
	Total     int        `json:"total"`
	Malformed int        `json:"malformed"`
	Groups    []CSPGroup `json:"groups"`
}

func DigestCSP(payloads [][]byte) CSPDigest {
	summary := CSPDigest{Groups: []CSPGroup{}}
	indices := map[CSPViolation]int{}
	for _, raw := range payloads {
		rows, err := ParseCSP(raw)
		if err != nil {
			summary.Malformed++
			continue
		}
		for _, row := range rows {
			summary.Total++
			index, ok := indices[row]
			if !ok {
				indices[row] = len(summary.Groups)
				summary.Groups = append(summary.Groups, CSPGroup{1, row.Directive, row.Blocked, row.Document})
			} else {
				summary.Groups[index].Count++
			}
		}
	}
	sort.SliceStable(summary.Groups, func(i, j int) bool { return summary.Groups[i].Count > summary.Groups[j].Count })
	return summary
}

// ReadCSPDigest accepts the legacy operator JSON/JSONL/stdin contract. It retains
// only sanitized triples and caps the input independently of browser ingestion.
func ReadCSPDigest(reader io.Reader) (CSPDigest, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, (16<<20)+1))
	if err != nil || len(raw) > 16<<20 {
		return CSPDigest{}, errors.New("CSP digest input exceeds limit")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return DigestCSP(nil), nil
	}
	payloads := [][]byte{}
	if json.Valid(raw) {
		var entries []json.RawMessage
		if len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '[' && json.Unmarshal(raw, &entries) == nil {
			for _, entry := range entries {
				payloads = append(payloads, entry)
			}
		} else {
			payloads = append(payloads, raw)
		}
	} else {
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		scanner.Buffer(make([]byte, 4096), 16<<20)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) != "" {
				payloads = append(payloads, append([]byte(nil), scanner.Bytes()...))
			}
			if len(payloads) > 100000 {
				return CSPDigest{}, errors.New("CSP digest report bound exceeded")
			}
		}
		if scanner.Err() != nil {
			return CSPDigest{}, errors.New("CSP digest input exceeds limit")
		}
	}
	return DigestCSP(payloads), nil
}
