package web

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Test-only source epoch bridge. The final table is generated exactly once
// after all reviewed product and nonassertion adapter source is frozen.
// No original registry, relation, assertion or physical input tuple is edited.
//
//go:embed testdata/native-completion-source.json
var completionSourceBytes []byte

type completionSourceSplice struct {
	PriorStart, PriorEnd, CurrentStart, CurrentEnd int
	Before, After                                  string
}
type completionSourceRow struct {
	Path, CurrentSHA256, PriorSHA256 string
	CurrentBytes, PriorBytes         int
	CurrentMode                      uint32
	Splices                          []completionSourceSplice
}
type completionSourceTable struct {
	Schema       int
	SourceCommit string
	Rows         []completionSourceRow
}

var loadCompletionSourceTable = sync.OnceValues(func() (map[string]completionSourceRow, error) {
	var table completionSourceTable
	decoder := json.NewDecoder(bytes.NewReader(completionSourceBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&table); err != nil {
		return nil, err
	}
	var tail any
	if decoder.Decode(&tail) != io.EOF {
		return nil, fmt.Errorf("completion-source-trailing-json")
	}
	if table.Schema != 1 || table.SourceCommit != "a2676cfb5326cf9a3b232bd916bbd32bf313e50e" || len(table.Rows) == 0 {
		return nil, fmt.Errorf("completion-source-frame")
	}
	rows := map[string]completionSourceRow{}
	for _, row := range table.Rows {
		if row.Path == "" || filepath.IsAbs(row.Path) || filepath.ToSlash(filepath.Clean(row.Path)) != row.Path || strings.HasPrefix(row.Path, "../") || strings.Contains(row.Path, "\\") || row.CurrentBytes < 1 || row.PriorBytes < 1 || len(row.CurrentSHA256) != 64 || len(row.PriorSHA256) != 64 || row.CurrentSHA256 == row.PriorSHA256 || len(row.Splices) == 0 || row.CurrentMode&0o170000 != 0o100000 || row.CurrentMode&0o7000 != 0 {
			return nil, fmt.Errorf("completion-source-row")
		}
		if _, exists := rows[row.Path]; exists {
			return nil, fmt.Errorf("completion-source-duplicate")
		}
		previousCurrent, previousPrior := 0, 0
		for _, splice := range row.Splices {
			if splice.CurrentStart < previousCurrent || splice.PriorStart < previousPrior || splice.CurrentEnd < splice.CurrentStart || splice.PriorEnd < splice.PriorStart || splice.CurrentEnd > row.CurrentBytes || splice.PriorEnd > row.PriorBytes || len(splice.After) != splice.CurrentEnd-splice.CurrentStart || len(splice.Before) != splice.PriorEnd-splice.PriorStart {
				return nil, fmt.Errorf("completion-source-splice")
			}
			previousCurrent, previousPrior = splice.CurrentEnd, splice.PriorEnd
		}
		rows[row.Path] = row
	}
	return rows, nil
})

// Strict for every changed protected path. Unchanged paths retain the existing
// original registry's identity relation and all its existing mode/hash checks.
func completionSourcePreimage(path string, raw []byte) ([]byte, error) {
	rows, err := loadCompletionSourceTable()
	if err != nil {
		return nil, err
	}
	row, changed := rows[path]
	if !changed {
		return append([]byte(nil), raw...), nil
	}
	if len(raw) != row.CurrentBytes || guiPublicHash(raw) != row.CurrentSHA256 {
		return nil, fmt.Errorf("completion-complete-current-source: %s", path)
	}
	prior := append([]byte(nil), raw...)
	for i := len(row.Splices) - 1; i >= 0; i-- {
		splice := row.Splices[i]
		if !bytes.Equal(prior[splice.CurrentStart:splice.CurrentEnd], []byte(splice.After)) {
			return nil, fmt.Errorf("completion-exact-current-splice: %s", path)
		}
		next := make([]byte, 0, len(prior)-len(splice.After)+len(splice.Before))
		next = append(next, prior[:splice.CurrentStart]...)
		next = append(next, splice.Before...)
		next = append(next, prior[splice.CurrentEnd:]...)
		prior = next
	}
	if len(prior) != row.PriorBytes || guiPublicHash(prior) != row.PriorSHA256 {
		return nil, fmt.Errorf("completion-exact-a267-preimage: %s", path)
	}
	return prior, nil
}

// Only internal retained-epoch functions use this stage: strip the exact final
// image once; otherwise leave bytes to their original strict hash predicates.
// It is not an admission predicate and is never used to replace a live tuple.
func completionSourceStage(path string, raw []byte) ([]byte, error) {
	rows, err := loadCompletionSourceTable()
	if err != nil {
		return nil, err
	}
	row, exists := rows[path]
	if exists && len(raw) == row.CurrentBytes && guiPublicHash(raw) == row.CurrentSHA256 {
		return completionSourcePreimage(path, raw)
	}
	return append([]byte(nil), raw...), nil
}

func completionSourceBindingMatches(path string, raw []byte, physical, wanted guiG1Binding) bool {
	if len(raw) != physical.Bytes || guiPublicHash(raw) != physical.SHA256 || !guiPublicGetLiveMode(physical.FullMode, wanted.FullMode) {
		return false
	}
	prior, err := completionSourcePreimage(path, raw)
	return err == nil && guiPublicGetBindingLive(guiG1Binding{len(prior), guiPublicHash(prior), physical.FullMode}, wanted)
}

// Map one unique a267 literal to its real final-source span. A literal boundary
// inside a replaced fragment is ambiguous and refuses; no target is invented.
func completionMutationTarget(path string, physical []byte, priorTarget string) (string, error) {
	rows, err := loadCompletionSourceTable()
	if err != nil {
		return "", err
	}
	row, changed := rows[path]
	if !changed {
		if priorTarget == "" || bytes.Count(physical, []byte(priorTarget)) != 1 {
			return "", fmt.Errorf("completion-unique-unchanged-target")
		}
		return priorTarget, nil
	}
	prior, err := completionSourcePreimage(path, physical)
	if err != nil {
		return "", err
	}
	if priorTarget == "" || bytes.Count(prior, []byte(priorTarget)) != 1 {
		return "", fmt.Errorf("completion-unique-prior-target")
	}
	start := bytes.Index(prior, []byte(priorTarget))
	end := start + len(priorTarget)
	currentStart, currentEnd := start, end
	for _, splice := range row.Splices {
		delta := len(splice.After) - len(splice.Before)
		if splice.PriorEnd <= start {
			currentStart += delta
		} else if splice.PriorStart < start && start < splice.PriorEnd {
			return "", fmt.Errorf("completion-target-start-interior")
		}
		if splice.PriorEnd <= end {
			currentEnd += delta
		} else if splice.PriorStart < end && end < splice.PriorEnd {
			return "", fmt.Errorf("completion-target-end-interior")
		}
	}
	if currentStart < 0 || currentEnd <= currentStart || currentEnd > len(physical) {
		return "", fmt.Errorf("completion-current-target-range")
	}
	target := string(physical[currentStart:currentEnd])
	if bytes.Count(physical, []byte(target)) != 1 {
		return "", fmt.Errorf("completion-unique-current-target")
	}
	return target, nil
}

func TestNativeCompletionAggregateSourceRequiresEveryExactPhysicalImage(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := loadCompletionSourceTable()
	if err != nil {
		t.Fatal(err)
	}
	for path, row := range rows {
		physical, binding, err := guiCurrentTransportReadBinding(root, path, 2<<20)
		if err != nil || binding.FullMode&0o7000 != 0 || !guiPublicGetLiveMode(binding.FullMode, row.CurrentMode) {
			t.Fatal("physical completion binding unavailable", path, err)
		}
		before := append([]byte(nil), physical...)
		prior, err := completionSourcePreimage(path, physical)
		if err != nil || len(prior) != row.PriorBytes || guiPublicHash(prior) != row.PriorSHA256 || !bytes.Equal(before, physical) {
			t.Fatal("exact full a267 source not recovered", path, err)
		}
		for label, changed := range map[string][]byte{"historical-is-not-current": prior, "unrelated-LF": append(append([]byte(nil), physical...), '\n'), "truncated": physical[:len(physical)-1]} {
			t.Run(filepath.Base(path)+"/"+label, func(t *testing.T) {
				if _, err := completionSourcePreimage(path, changed); err == nil {
					t.Fatal("unsupported complete image admitted")
				}
			})
		}
		for _, splice := range row.Splices {
			if splice.Before != "" && bytes.Count(prior, []byte(splice.Before)) == 1 {
				target, err := completionMutationTarget(path, physical, splice.Before)
				if err == nil {
					changed := bytes.Replace(physical, []byte(target), nil, 1)
					if bytes.Equal(changed, physical) {
						t.Fatal("mapped physical mutation became a no-op")
					}
					if _, err := completionSourcePreimage(path, changed); err == nil {
						t.Fatal("real mapped mutation admitted")
					}
				}
			}
		}
		if again, err := os.ReadFile(filepath.Join(root, path)); err != nil || !bytes.Equal(again, physical) {
			t.Fatal("source verification wrote owning bytes", path)
		}
	}
}
