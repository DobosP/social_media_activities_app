package web

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Only complete literal afterimages enter current physical readers. Explicit
// retained prior controls may consume the exact recovered source, never drift.
func accountCenterSourceRelation(path string) (beforeSHA, afterSHA string, beforeBytes, afterBytes int, before, after string, ok bool) {
	switch path {
	case "services/server/internal/web/renderer.go":
		return "f7d5a2ea7b1cd3f87b2c6ee3af1aa1800f588d14837448a3fcacab381b3f80aa", "83a083345a20d2a650d3b2500c4081da41783a631a6e62092ef5e872be78f181", 6535, 6744, "\t} else if name == \"web/notifications.html\" {", "\t} else if name == \"web/you.html\" || name == \"web/settings.html\" {\n\t\traw, renderErr := r.renderAccountCenter(request, name, defaults)\n\t\tif renderErr != nil {\n\t\t\treturn renderErr\n\t\t}\n\t\t_, _ = output.Write(raw)\n\t} else if name == \"web/notifications.html\" {", true
	case "services/server/internal/web/gui_public_get_walk_test.go":
		return "1e2b7e65a9b4a171d328c820364fd9c0b5e3de8a8fb33dd5881271b9fd8115fe", "2ed35fb9d5ac6deef01f48fbfd05fe24590d774e8cfeee9fa8769869742ac171", 63161, 63507, "func guiPublicGetRowsPreimage(path string, raw []byte) ([]byte, error) {\n", "func guiPublicGetRowsPreimage(path string, raw []byte) ([]byte, error) {\n\t// Require the complete newest account source before the unchanged prior chain.\n\tswitch path {\n\tcase \"services/server/internal/web/renderer.go\", \"services/server/internal/web/gui_public_get_walk_test.go\":\n\t\tvar accountErr error\n\t\traw, accountErr = accountCenterSourcePreimage(path, raw)\n\t\tif accountErr != nil {\n\t\t\treturn nil, accountErr\n\t\t}\n\t}\n", true
	}
	return "", "", 0, 0, "", "", false
}
func accountCenterSourcePreimage(path string, raw []byte) ([]byte, error) {
	beforeSHA, afterSHA, beforeBytes, afterBytes, before, after, ok := accountCenterSourceRelation(path)
	if !ok || len(raw) != afterBytes || guiPublicHash(raw) != afterSHA || bytes.Count(raw, []byte(after)) != 1 {
		return nil, fmt.Errorf("account-complete-current-source")
	}
	previous := bytes.Replace(append([]byte(nil), raw...), []byte(after), []byte(before), 1)
	if len(previous) != beforeBytes || guiPublicHash(previous) != beforeSHA {
		return nil, fmt.Errorf("account-exact-source-preimage")
	}
	return previous, nil
}
func accountCenterPriorSource(path string, raw []byte) ([]byte, error) {
	beforeSHA, _, beforeBytes, _, _, _, ok := accountCenterSourceRelation(path)
	if !ok || len(raw) == beforeBytes && guiPublicHash(raw) == beforeSHA {
		return append([]byte(nil), raw...), nil
	}
	return accountCenterSourcePreimage(path, raw)
}

// Extend only the retained inbox negative fixture after its original complete
// inverse/authority checks. Every live reader still requires newest source.
func accountCenterInboxMutationTarget(path string, physical []byte, original string) (string, error) {
	previous, err := accountCenterSourcePreimage(path, physical)
	if err != nil {
		return "", err
	}
	_, _, _, _, _, expected, ok := notificationInboxSourceRelation(path)
	if !ok || original != expected || bytes.Count(previous, []byte(original)) != 1 {
		return "", fmt.Errorf("account-inbox-original-mutation-target")
	}
	target := original
	if path == "services/server/internal/web/gui_public_get_walk_test.go" {
		_, _, _, _, before, after, mapped := accountCenterSourceRelation(path)
		if !mapped || before == "" || !bytes.HasPrefix([]byte(original), []byte(before)) {
			return "", fmt.Errorf("account-inbox-mapped-mutation-prefix")
		}
		target = after + original[len(before):]
	}
	if target == "" || bytes.Count(physical, []byte(target)) != 1 {
		return "", fmt.Errorf("account-inbox-current-mutation-target")
	}
	return target, nil
}

func TestNativeAccountCenterCustodyRetainsStrictCurrentAndAllPriorSources(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal("source root unavailable")
	}
	var registry guiPreferenceGETRegistry
	if guiG1StrictJSON(guiPreferencesGETRegistryBytes, &registry) != nil {
		t.Fatal("original registry unavailable")
	}
	for _, path := range []string{"services/server/internal/web/renderer.go", "services/server/internal/web/gui_public_get_walk_test.go"} {
		physical, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal("physical source unavailable")
		}
		previous, err := accountCenterSourcePreimage(path, physical)
		if err != nil {
			t.Fatal("new complete source inverse refused")
		}
		_, _, _, _, _, after, _ := accountCenterSourceRelation(path)
		historical, err := guiPublicGetRowsPreimage(path, physical)
		if err != nil {
			t.Fatal("complete original source chain refused")
		}
		old, oldErr := notificationInboxSourcePreimage(path, previous)
		current, currentErr := notificationInboxSourcePreimage(path, physical)
		if oldErr != nil || currentErr != nil || !bytes.Equal(old, current) {
			t.Fatal("retained inbox authority changed")
		}
		_, _, _, _, _, originalTarget, _ := notificationInboxSourceRelation(path)
		currentTarget, err := accountCenterInboxMutationTarget(path, physical, originalTarget)
		if err != nil || bytes.Count(physical, []byte(currentTarget)) != 1 || bytes.Equal(physical, bytes.Replace(physical, []byte(currentTarget), nil, 1)) {
			t.Fatal("retained negative fixture did not uniquely mutate newest source")
		}
		if _, err := accountCenterInboxMutationTarget(path, physical, originalTarget+" "); err == nil {
			t.Fatal("unsupported old mutation literal admitted")
		}
		for _, changed := range [][]byte{
			previous, append(append([]byte(nil), physical...), '\n'), physical[:len(physical)-1],
			bytes.Replace(physical, []byte(after), nil, 1), append(append([]byte(nil), physical...), []byte(after)...),
			bytes.Replace(physical, []byte(after), []byte(after+" "), 1),
		} {
			if bytes.Equal(physical, changed) {
				t.Fatal("negative source probe became a no-op")
			}
			if _, err := accountCenterInboxMutationTarget(path, changed, originalTarget); err == nil {
				t.Fatal("retained negative fixture admitted old or modified physical source")
			}
			if _, err := accountCenterSourcePreimage(path, changed); err == nil {
				t.Fatal("old, rehashed or malformed current source admitted")
			}
			if _, err := guiPublicGetRowsPreimage(path, changed); err == nil {
				t.Fatal("live reader lost exact physical epoch")
			}
			binding := guiG1Binding{len(changed), guiPublicHash(changed), 0o100644}
			if _, err := groupCreatePreferenceSourceBinding(path, changed, binding, registry.Sources[path]); err == nil {
				t.Fatal("rehashed physical tuple admitted")
			}
			if again, err := guiPublicGetRowsPreimage(path, physical); err != nil || !bytes.Equal(again, historical) {
				t.Fatal("negative probe mutated source custody")
			}
		}
	}
	inputs, err := guiPreferenceGETInputs(root, registry)
	if err != nil || len(inputs) != 138 {
		t.Fatal("all138 real physical tuples required")
	}
	for path, binding := range inputs {
		_, actual, err := guiCurrentTransportReadBinding(root, path, 2<<20)
		if err != nil || binding != actual {
			t.Fatal("historical metadata replaced physical current input")
		}
	}
	rows := guiPublicGetRepairRows()
	if len(rows) != 32 || len(groupDetailPreviousRepairRows(rows)) != 31 || len(groupCreatePreviousRepairRows()) != 27 {
		t.Fatal("original32/31/27 relation chain changed")
	}
}
