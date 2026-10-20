package web

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Newest physical registration/walker images must be complete. Only an exact
// inverse can enter the unchanged account/inbox/original historical chain.
func languageActionSourceRelation(path string) (beforeSHA, afterSHA string, beforeBytes, afterBytes int, before, after string, ok bool) {
	switch path {
	case "services/server/internal/web/server.go":
		return "5a55f84558c62d240fb48d9f147fea623ec863435696b4b6eb50127664f4c340", "36e32c94561e12c5f8db040a9e303822c76d0a922e9e3644e64c42776740d39a", 11113, 11223, "func (s *Server) Register(mux *http.ServeMux) {\n", "func (s *Server) Register(mux *http.ServeMux) {\n\tmux.HandleFunc(\"POST /i18n/setlang/{$}\", s.setLanguage)\n\tmux.HandleFunc(\"POST /i18n/setlang\", http.NotFound)\n", true
	case "services/server/internal/app/app.go":
		return "bafbe950fe966cff748edd59c6961ba1c1cd023b2857a4117d8a1b03bd2b3cec", "d7024cbe932f92e8590f3db1aa4bfd201192973dee0e9ed79e4e9210bc87ddc5", 22947, 23179, "\t// Declared oversize is rejected before authentication, decoding or disk\n", "\t// Bound this exact form before the existing session-CSRF parser too.\n\tif r.Method == http.MethodPost && r.URL.Path == \"/i18n/setlang/\" && r.URL.EscapedPath() == \"/i18n/setlang/\" {\n\t\tlimit = min(limit, web.LanguageFormMaxBytes)\n\t}\n\t// Declared oversize is rejected before authentication, decoding or disk\n", true
	case "services/server/internal/web/gui_public_get_walk_test.go":
		return "2ed35fb9d5ac6deef01f48fbfd05fe24590d774e8cfeee9fa8769869742ac171", "02ad521217b8db6a6c4688e3b5a30403256dc0807f03b8a8d4c70897bb040a74", 63507, 63953, "func guiPublicGetRowsPreimage(path string, raw []byte) ([]byte, error) {\n", "func guiPublicGetRowsPreimage(path string, raw []byte) ([]byte, error) {\n\t// Latest language registration/current walker must be exact before the\n\t// unchanged account/inbox and original historical source chain.\n\tswitch path {\n\tcase \"services/server/internal/web/server.go\", \"services/server/internal/app/app.go\", \"services/server/internal/web/gui_public_get_walk_test.go\":\n\t\tvar languageErr error\n\t\traw, languageErr = languageActionSourcePreimage(path, raw)\n\t\tif languageErr != nil {\n\t\t\treturn nil, languageErr\n\t\t}\n\t}\n", true
	}
	return "", "", 0, 0, "", "", false
}
func languageActionSourcePreimage(path string, raw []byte) ([]byte, error) {
	// Ordinary current reads require the complete repaired walker first. Its
	// exact failed-L1 preimage then enters the unchanged language/history chain.
	var repairErr error
	raw, repairErr = languageActionRepairWalkerPreimage(path, raw)
	if repairErr != nil {
		return nil, repairErr
	}
	beforeSHA, afterSHA, beforeBytes, afterBytes, before, after, ok := languageActionSourceRelation(path)
	if !ok || len(raw) != afterBytes || guiPublicHash(raw) != afterSHA || bytes.Count(raw, []byte(after)) != 1 {
		return nil, fmt.Errorf("language-complete-current-source")
	}
	previous := bytes.Replace(append([]byte(nil), raw...), []byte(after), []byte(before), 1)
	if len(previous) != beforeBytes || guiPublicHash(previous) != beforeSHA {
		return nil, fmt.Errorf("language-exact-prior-source")
	}
	return previous, nil
}
func languageActionPriorSource(path string, raw []byte) ([]byte, error) {
	beforeSHA, _, beforeBytes, _, _, _, ok := languageActionSourceRelation(path)
	if !ok || len(raw) == beforeBytes && guiPublicHash(raw) == beforeSHA {
		return append([]byte(nil), raw...), nil
	}
	return languageActionSourcePreimage(path, raw)
}
func languageActionAccountMutationTarget(path string, physical []byte, original string) (string, error) {
	_, _, _, _, _, expected, mapped := accountCenterSourceRelation(path)
	if !mapped || original != expected {
		return "", fmt.Errorf("language-original-account-target")
	}
	target := original
	switch path {
	case "services/server/internal/web/gui_public_get_walk_test.go":
		if _, err := languageActionSourcePreimage(path, physical); err != nil {
			return "", err
		}
		_, _, _, _, before, after, ok := languageActionSourceRelation(path)
		if !ok || !bytes.HasPrefix([]byte(original), []byte(before)) {
			return "", fmt.Errorf("language-account-target-prefix")
		}
		target = after + original[len(before):]
	case "services/server/internal/web/renderer.go":
		if _, err := accountCenterSourcePreimage(path, physical); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("language-target-scope")
	}
	if target == "" || bytes.Count(physical, []byte(target)) != 1 {
		return "", fmt.Errorf("language-unique-current-account-target")
	}
	return target, nil
}
func TestNativeLanguageCustodyRequiresNewestSourceAndRetainsHistoricalInverses(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal("source root unavailable")
	}
	for _, path := range []string{"services/server/internal/web/server.go", "services/server/internal/app/app.go", "services/server/internal/web/gui_public_get_walk_test.go"} {
		physical, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal("physical source unavailable")
		}
		previous, err := languageActionSourcePreimage(path, physical)
		if err != nil {
			t.Fatal("new complete inverse refused")
		}
		beforeSHA, _, beforeBytes, _, _, after, _ := languageActionSourceRelation(path)
		if len(previous) != beforeBytes || guiPublicHash(previous) != beforeSHA {
			t.Fatal("whole prior source not recovered")
		}
		historical, err := guiPublicGetRowsPreimage(path, physical)
		if err != nil || len(historical) == 0 {
			t.Fatal("unchanged complete historical chain refused")
		}
		if _, err := guiPublicGetRowsPreimage(path, previous); err == nil {
			t.Fatal("ordinary current reader admitted previous source")
		}
		for _, changed := range [][]byte{previous, append(append([]byte(nil), physical...), '\n'), physical[:len(physical)-1], bytes.Replace(physical, []byte(after), nil, 1), append(append([]byte(nil), physical...), []byte(after)...), bytes.Replace(physical, []byte(after), []byte(after+" "), 1)} {
			if bytes.Equal(physical, changed) {
				t.Fatal("negative source probe became a no-op")
			}
			if _, err := languageActionSourcePreimage(path, changed); err == nil {
				t.Fatal("modified/old current source admitted")
			}
			if _, err := guiPublicGetRowsPreimage(path, changed); err == nil {
				t.Fatal("ordinary historical reader bypassed new physical gate")
			}
		}
	}
}
func TestNativeLanguageAccountMutationBridgeKeepsEveryPriorTargetReal(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal("source root unavailable")
	}
	for _, path := range []string{"services/server/internal/web/renderer.go", "services/server/internal/web/gui_public_get_walk_test.go"} {
		physical, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal("physical source unavailable")
		}
		_, _, _, _, _, old, _ := accountCenterSourceRelation(path)
		target, err := languageActionAccountMutationTarget(path, physical, old)
		if err != nil || bytes.Count(physical, []byte(target)) != 1 || bytes.Equal(physical, bytes.Replace(physical, []byte(target), nil, 1)) {
			t.Fatal("old account target lost its real current mutation")
		}
		if _, err := languageActionAccountMutationTarget(path, physical, old+" "); err == nil {
			t.Fatal("unsupported old target admitted")
		}
		if _, err := languageActionAccountMutationTarget(path, append(append([]byte(nil), physical...), '\n'), old); err == nil {
			t.Fatal("unrelated current source drift admitted")
		}
	}
}

// This additive relation preserves the exact failed L1 walker bytes as history.
// It admits only the complete newest physical source, never an old/mixed epoch.
func languageActionRepairWalkerRelation() (beforeSHA, afterSHA string, beforeBytes, afterBytes int, before, after string) {
	return "02ad521217b8db6a6c4688e3b5a30403256dc0807f03b8a8d4c70897bb040a74", "825ca4ef550e24cf0c0005187125c57ff47789e10c8e2ff5cbca4cba24441d7a", 63953, 64366, "\t\tpreimages[name] = input.raw\n", "\t\tpreimages[name] = input.raw\n\t\t// App is preserved in the93-input frame but has no original repair row.\n\t\t// Recover only its exact language-limit preimage for this historical proof;\n\t\t// the input map and every live physical tuple remain unchanged.\n\t\tif name == \"services/server/internal/app/app.go\" {\n\t\t\toriginal, err := languageActionSourcePreimage(name, input.raw)\n\t\t\tif err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tpreimages[name] = original\n\t\t}\n"
}

func languageActionRepairWalkerPreimage(path string, raw []byte) ([]byte, error) {
	if path != "services/server/internal/web/gui_public_get_walk_test.go" {
		return append([]byte(nil), raw...), nil
	}
	beforeSHA, afterSHA, beforeBytes, afterBytes, before, after := languageActionRepairWalkerRelation()
	if len(raw) != afterBytes || guiPublicHash(raw) != afterSHA || bytes.Count(raw, []byte(after)) != 1 {
		return nil, fmt.Errorf("language-repair-complete-current-walker")
	}
	previous := bytes.Replace(append([]byte(nil), raw...), []byte(after), []byte(before), 1)
	if len(previous) != beforeBytes || guiPublicHash(previous) != beforeSHA {
		return nil, fmt.Errorf("language-repair-exact-failed-epoch")
	}
	return previous, nil
}

func TestNativeLanguageRepairWalkerRetainsExactFailedEpochAndRefusals(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal("source root unavailable")
	}
	const path = "services/server/internal/web/gui_public_get_walk_test.go"
	physical, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal("physical walker unavailable")
	}
	previous, err := languageActionRepairWalkerPreimage(path, physical)
	beforeSHA, _, beforeBytes, _, _, after := languageActionRepairWalkerRelation()
	if err != nil || len(previous) != beforeBytes || guiPublicHash(previous) != beforeSHA {
		t.Fatal("exact failed source epoch not retained")
	}
	historical, err := guiPublicGetRowsPreimage(path, physical)
	if err != nil || len(historical) == 0 {
		t.Fatal("unchanged historical chain refused")
	}
	for name, raw := range map[string][]byte{
		"failed-epoch-is-not-current": previous,
		"unrelated-LF":                append(append([]byte(nil), physical...), '\n'),
		"truncated":                   physical[:len(physical)-1],
		"missing-inverse-literal":     bytes.Replace(physical, []byte(after), nil, 1),
		"duplicated-inverse-literal":  append(append([]byte(nil), physical...), []byte(after)...),
		"modified-inverse-literal":    bytes.Replace(physical, []byte(after), []byte(after+" "), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(raw, physical) {
				t.Fatal("new source probe became a no-op")
			}
			if _, err := languageActionRepairWalkerPreimage(path, raw); err == nil {
				t.Fatal("old or modified walker admitted")
			}
			if _, err := guiPublicGetRowsPreimage(path, raw); err == nil {
				t.Fatal("ordinary source reader bypassed repaired physical epoch")
			}
		})
	}
	if again, err := os.ReadFile(filepath.Join(root, path)); err != nil || !bytes.Equal(again, physical) {
		t.Fatal("physical walker changed during memory-only controls")
	}
}

func TestNativeLanguageRepairHistoricalAppProofKeepsPhysicalTuples(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	source, _, sourceErr := guiPublicGetSources(guiPublicGetSourceBytes)
	inputs, inputErr := guiPublicGetRepairInputs(root, source)
	if err != nil || sourceErr != nil || inputErr != nil || len(inputs) != 93 || guiPublicGetRepairHistoricalProof(source, inputs) != nil {
		t.Fatal("complete current historical proof unavailable")
	}
	const path = "services/server/internal/app/app.go"
	input := inputs[path]
	prior, err := languageActionSourcePreimage(path, input.raw)
	matched := false
	for _, row := range source.PreservedSourceInputs {
		if row.Path == path {
			matched = err == nil && len(prior) == row.Bytes && guiPublicHash(prior) == row.SHA256
		}
	}
	if !matched || input.binding.Bytes != len(input.raw) || input.binding.SHA256 != guiPublicHash(input.raw) || bytes.Equal(prior, input.raw) {
		t.Fatal("historical app proof replaced a physical current tuple")
	}
	_, _, _, _, _, after, ok := languageActionSourceRelation(path)
	if !ok || bytes.Count(input.raw, []byte(after)) != 1 {
		t.Fatal("real app limit mutation target unavailable")
	}
	for name, raw := range map[string][]byte{
		"old-app-is-not-current":   prior,
		"unrelated-LF":             append(append([]byte(nil), input.raw...), '\n'),
		"truncated":                input.raw[:len(input.raw)-1],
		"missing-limit-literal":    bytes.Replace(input.raw, []byte(after), nil, 1),
		"duplicated-limit-literal": append(append([]byte(nil), input.raw...), []byte(after)...),
		"modified-limit-literal":   bytes.Replace(input.raw, []byte(after), []byte(after+" "), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(raw, input.raw) {
				t.Fatal("app source probe became a no-op")
			}
			changed := map[string]guiPublicGetRepairInput{}
			for path, original := range inputs {
				changed[path] = original
			}
			binding := input.binding
			binding.Bytes, binding.SHA256 = len(raw), guiPublicHash(raw)
			changed[path] = guiPublicGetRepairInput{raw, binding}
			if guiPublicGetRepairHistoricalProof(source, changed) == nil {
				t.Fatal("rehashed old or modified app source admitted")
			}
			if guiPublicGetRepairHistoricalProof(source, inputs) != nil {
				t.Fatal("negative probe mutated original proof")
			}
		})
	}
	if inputs[path].binding != input.binding || !bytes.Equal(inputs[path].raw, input.raw) {
		t.Fatal("physical input map changed")
	}
	if again, err := os.ReadFile(filepath.Join(root, path)); err != nil || !bytes.Equal(again, input.raw) {
		t.Fatal("physical app source changed")
	}
}
