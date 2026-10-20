package web

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Newest source custody only. Physical readers require the exact full current
// afterimage. Only explicitly retained controls consume its recovered old epoch.
func notificationInboxSourceRelation(path string) (beforeSHA, afterSHA string, beforeBytes, afterBytes int, before, after string, ok bool) {
	switch path {
	case "services/server/internal/web/renderer.go":
		return "367b05a8a05828b75181ff2d1cc4a2164f936758342a3b53dee777496c3314ba", "f7d5a2ea7b1cd3f87b2c6ee3af1aa1800f588d14837448a3fcacab381b3f80aa", 6349, 6535, "\t} else if name == \"web/notification_preferences.html\" {", "\t} else if name == \"web/notifications.html\" {\n\t\traw, renderErr := r.renderNotificationInbox(request, defaults)\n\t\tif renderErr != nil {\n\t\t\treturn renderErr\n\t\t}\n\t\t_, _ = output.Write(raw)\n\t} else if name == \"web/notification_preferences.html\" {", true
	case "services/server/internal/web/gui_public_get_walk_test.go":
		return "ba643be27641c54da5c7f261519ef5ab83203b7e7f4287974db430d793f3f428", "1e2b7e65a9b4a171d328c820364fd9c0b5e3de8a8fb33dd5881271b9fd8115fe", 62819, 63161, "func guiPublicGetRowsPreimage(path string, raw []byte) ([]byte, error) {\n", "func guiPublicGetRowsPreimage(path string, raw []byte) ([]byte, error) {\n\t// Require the actual newest physical source, then retain the entire old chain.\n\tswitch path {\n\tcase \"services/server/internal/web/renderer.go\", \"services/server/internal/web/gui_public_get_walk_test.go\":\n\t\tvar inboxErr error\n\t\traw, inboxErr = notificationInboxSourcePreimage(path, raw)\n\t\tif inboxErr != nil {\n\t\t\treturn nil, inboxErr\n\t\t}\n\t}\n", true
	}
	return "", "", 0, 0, "", "", false
}
func notificationInboxSourcePreimage(path string, raw []byte) ([]byte, error) {
	// Explicit retained controls may enter only on the exact account preimage.
	var accountErr error
	raw, accountErr = accountCenterPriorSource(path, raw)
	if accountErr != nil {
		return nil, accountErr
	}
	beforeSHA, afterSHA, beforeBytes, afterBytes, before, after, ok := notificationInboxSourceRelation(path)
	if !ok || len(raw) != afterBytes || guiPublicHash(raw) != afterSHA || bytes.Count(raw, []byte(after)) != 1 {
		return nil, fmt.Errorf("inbox-complete-current-source")
	}
	previous := bytes.Replace(append([]byte(nil), raw...), []byte(after), []byte(before), 1)
	if len(previous) != beforeBytes || guiPublicHash(previous) != beforeSHA {
		return nil, fmt.Errorf("inbox-exact-source-preimage")
	}
	return previous, nil
}
func notificationInboxPriorSource(path string, raw []byte) ([]byte, error) {
	beforeSHA, _, beforeBytes, _, _, _, ok := notificationInboxSourceRelation(path)
	if !ok {
		return append([]byte(nil), raw...), nil
	}
	if len(raw) == beforeBytes && guiPublicHash(raw) == beforeSHA {
		return append([]byte(nil), raw...), nil
	}
	return notificationInboxSourcePreimage(path, raw)
}

func TestNativeNotificationInboxCustodyRetainsEveryOriginalAuthority(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	var registry guiPreferenceGETRegistry
	if guiG1StrictJSON(guiPreferencesGETRegistryBytes, &registry) != nil {
		t.Fatal("immutable preference registry unavailable")
	}
	for _, path := range []string{"services/server/internal/web/renderer.go", "services/server/internal/web/gui_public_get_walk_test.go"} {
		physical, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		before, err := notificationInboxSourcePreimage(path, physical)
		if err != nil {
			t.Fatal("new whole-source inverse refused", path, err)
		}
		_, _, _, _, _, after, _ := notificationInboxSourceRelation(path)
		historical, err := guiPublicGetRowsPreimage(path, physical)
		if err != nil {
			t.Fatal("new plus complete original chain refused", path, err)
		}
		old, err := notificationPreferencesSourcePreimage(path, before)
		if err != nil {
			t.Fatal("explicit old control epoch unavailable", path, err)
		}
		current, err := notificationPreferencesSourcePreimage(path, physical)
		if err != nil || !bytes.Equal(old, current) {
			t.Fatal("new inverse changed a previous authority", path, err)
		}
		after, err = accountCenterInboxMutationTarget(path, physical, after)
		if err != nil {
			t.Fatal("current inbox mutation fixture unavailable", path, err)
		}
		for name, changed := range map[string][]byte{
			"previous-is-not-physical": before,
			"unrelated-LF":             append(append([]byte(nil), physical...), '\n'),
			"truncated":                physical[:len(physical)-1],
			"missing-literal":          bytes.Replace(physical, []byte(after), nil, 1),
			"duplicate-literal":        append(append([]byte(nil), physical...), []byte(after)...),
			"modified-literal":         bytes.Replace(physical, []byte(after), []byte(after+" "), 1),
		} {
			t.Run(filepath.Base(path)+"/"+name, func(t *testing.T) {
				if bytes.Equal(physical, changed) {
					t.Fatal("source mutation became a no-op")
				}
				if _, err := notificationInboxSourcePreimage(path, changed); err == nil {
					t.Fatal("unsupported newest source admitted")
				}
				if _, err := guiPublicGetRowsPreimage(path, changed); err == nil {
					t.Fatal("old/rehashed source admitted as physical live input")
				}
				binding := guiG1Binding{len(changed), guiPublicHash(changed), 0o100644}
				if _, err := groupCreatePreferenceSourceBinding(path, changed, binding, registry.Sources[path]); err == nil {
					t.Fatal("rehashed old physical tuple admitted")
				}
				again, err := guiPublicGetRowsPreimage(path, physical)
				if err != nil || !bytes.Equal(again, historical) {
					t.Fatal("negative probe changed the current source", err)
				}
			})
		}
	}
	inputs, err := guiPreferenceGETInputs(root, registry)
	if err != nil || len(inputs) != 138 {
		t.Fatal("all138 real current physical tuples must remain", err)
	}
	for path, binding := range inputs {
		_, actual, err := guiCurrentTransportReadBinding(root, path, 2<<20)
		if err != nil || binding != actual {
			t.Fatal("historical tuple substituted for current physical input", path, err)
		}
	}
	rows := guiPublicGetRepairRows()
	if len(rows) != 32 || len(groupDetailPreviousRepairRows(rows)) != 31 || len(groupCreatePreviousRepairRows()) != 27 {
		t.Fatal("all old32/31/27 control inventories must remain")
	}
}
