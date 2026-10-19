package web

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Explicitly scoped to the recovered former family afterimage. The ordinary
// current-profile walker remains unchanged and still requires the newest row.
func groupCreateCustodyRowsPreimage(path string, raw []byte, rows []guiPublicGetRepairRow) ([]byte, error) {
	for _, row := range rows {
		if row.path != path {
			continue
		}
		if len(raw) != row.afterBytes || guiPublicHash(raw) != row.afterSHA || bytes.Count(raw, []byte(row.after)) != 1 {
			return nil, fmt.Errorf("repair-complete-current-profile")
		}
		raw = bytes.Replace(raw, []byte(row.after), []byte(row.before), 1)
		if len(raw) != row.beforeBytes || guiPublicHash(raw) != row.beforeSHA {
			return nil, fmt.Errorf("repair-exact-preimage")
		}
	}
	return raw, nil
}

func groupCreateFamilyCustodyPreimage(t *testing.T, raw []byte, rows []guiPublicGetRepairRow) ([]byte, []guiPublicGetRepairRow) {
	t.Helper()
	if len(rows) < 4 {
		t.Fatal("new group relation or existing family relations disappeared")
	}
	row := rows[0]
	if row.path != "services/server/internal/web/renderer.go" || row.beforeSHA != "e7c10feff706989b6141da3803e2ec4ff73c78a54f34c6e32db9e7b4937c6eee" || row.afterSHA != "6228270fd3343f7d352b79780bd9055ccbf9fe70b2d7917acdb4e0b6ffd31ed3" || row.beforeBytes != 5790 || row.afterBytes != 5967 || guiPublicHash(raw) != row.afterSHA || len(raw) != row.afterBytes || bytes.Count(raw, []byte(row.after)) != 1 {
		t.Fatal("physical renderer is not the exact admitted group afterimage")
	}
	previous := bytes.Replace(append([]byte(nil), raw...), []byte(row.after), []byte(row.before), 1)
	if len(previous) != row.beforeBytes || guiPublicHash(previous) != row.beforeSHA || rows[1].path != row.path || rows[1].afterSHA != row.beforeSHA {
		t.Fatal("new group inverse does not exactly recover the retained family afterimage")
	}
	return previous, rows[1:]
}

func TestNativeGroupCurrentCustodyExtendsAllOriginalRelationsAndRefusesDrift(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	const path = "services/server/internal/web/renderer.go"
	raw, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	rows := guiPublicGetRepairRows()
	previous, _ := groupCreateFamilyCustodyPreimage(t, raw, rows)
	historical, err := guiPublicGetRowsPreimage(path, raw)
	if err != nil || len(historical) != 5510 || guiPublicHash(historical) != "94f11a3f1849c919ba100c4ba68236263f71f07741006c1992dadc4020723e68" {
		t.Fatal("all current renderer relations no longer recover the original source", err)
	}
	for name, changed := range map[string][]byte{
		"missing-group-dispatch":                   bytes.Replace(raw, []byte(rows[0].after), nil, 1),
		"duplicate-group-dispatch":                 append(append([]byte(nil), raw...), []byte(rows[0].after)...),
		"modified-group-dispatch":                  bytes.Replace(raw, []byte("r.renderGroupCreate(request, defaults)"), []byte("r.renderGroupCreate(request, data)"), 1),
		"unrelated-byte":                           append(append([]byte(nil), raw...), '\n'),
		"former-family-current-is-not-new-current": previous,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := guiPublicGetRowsPreimage(path, changed); err == nil {
				t.Fatal("unsupported current renderer source admitted")
			}
			if got, err := guiPublicGetRowsPreimage(path, raw); err != nil || !bytes.Equal(got, historical) {
				t.Fatal("negative probe altered physical source custody", err)
			}
		})
	}
	const forms = "services/server/internal/web/forms.go"
	formSource, err := os.ReadFile(filepath.Join(root, forms))
	if err != nil {
		t.Fatal(err)
	}
	oldForms, err := guiPublicGetRowsPreimage(forms, formSource)
	if err != nil || len(oldForms) != 7324 || guiPublicHash(oldForms) != "6a41190d4f81107c32b7f0f651f59dfc6d1adc298b66eea3861105579360d3cb" {
		t.Fatal("semantic metadata did not invert to the complete original form builder", err)
	}
	for name, changed := range map[string][]byte{
		"unrelated-byte":              append(append([]byte(nil), formSource...), '\n'),
		"old-form-is-not-new-current": oldForms,
		"all-form-scope":              bytes.Replace(formSource, []byte(`if name == "GroupCreateForm" {`), []byte(`if name != "GroupCreateForm" {`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := guiPublicGetRowsPreimage(forms, changed); err == nil {
				t.Fatal("unsupported semantic metadata source admitted")
			}
		})
	}
}
