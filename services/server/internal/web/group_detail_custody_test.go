package web

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Only retained historical controls receive this exact31-row preimage. Live
// readers continue to consume every new physical tuple and the full32-row chain.
func groupDetailPreviousRepairRows(rows []guiPublicGetRepairRow) []guiPublicGetRepairRow {
	expected := guiPublicGetRepairRows()
	if len(expected) != 32 || len(rows) != len(expected) {
		return nil
	}
	row := expected[0]
	if row.path != "services/server/internal/web/renderer.go" || row.beforeSHA != "6228270fd3343f7d352b79780bd9055ccbf9fe70b2d7917acdb4e0b6ffd31ed3" || row.afterSHA != "3ba5b4456312437e8edf058b4f9967385bf14e483a0bb6d771b5881231d5c0e6" || row.beforeBytes != 5967 || row.afterBytes != 6146 || row.before != "\t} else {\n\t\ttemplate, parseErr := r.set.FromCache(name)" || row.after != "\t} else if name == \"web/group_detail.html\" {\n\t\traw, renderErr := r.renderGroupDetail(request, defaults)\n\t\tif renderErr != nil {\n\t\t\treturn renderErr\n\t\t}\n\t\t_, _ = output.Write(raw)\n\t} else {\n\t\ttemplate, parseErr := r.set.FromCache(name)" || expected[1].afterSHA != row.beforeSHA {
		return nil
	}
	for i := range expected {
		if rows[i] != expected[i] {
			return nil
		}
	}
	return append([]guiPublicGetRepairRow(nil), rows[1:]...)
}
func groupDetailRendererPreimage(raw []byte) ([]byte, error) {
	var notificationErr error
	raw, notificationErr = notificationPreferencesPriorSource("services/server/internal/web/renderer.go", raw)
	if notificationErr != nil {
		return nil, notificationErr
	}
	rows := guiPublicGetRepairRows()
	if groupDetailPreviousRepairRows(rows) == nil {
		return nil, fmt.Errorf("group-detail-current-rows")
	}
	row := rows[0]
	if len(raw) != row.afterBytes || guiPublicHash(raw) != row.afterSHA || bytes.Count(raw, []byte(row.after)) != 1 {
		return nil, fmt.Errorf("group-detail-complete-renderer")
	}
	before := bytes.Replace(append([]byte(nil), raw...), []byte(row.after), []byte(row.before), 1)
	if len(before) != row.beforeBytes || guiPublicHash(before) != row.beforeSHA {
		return nil, fmt.Errorf("group-detail-exact-renderer-preimage")
	}
	return before, nil
}
func groupDetailWalkerPreimage(raw []byte) ([]byte, error) {
	var notificationErr error
	raw, notificationErr = notificationPreferencesPriorSource("services/server/internal/web/gui_public_get_walk_test.go", raw)
	if notificationErr != nil {
		return nil, notificationErr
	}
	if len(raw) != 62298 || guiPublicHash(raw) != "8604ff0a20a1d2eb3ecff08ba5245124c7fccce7b9c223910db03cdef378f096" || bytes.Count(raw, []byte("\t\t// ADR-0063: exact whole group-detail dispatch; retain the complete31-row preimage.\n\t\t{\n\t\t\tpath:        \"services/server/internal/web/renderer.go\",\n\t\t\tbeforeSHA:   \"6228270fd3343f7d352b79780bd9055ccbf9fe70b2d7917acdb4e0b6ffd31ed3\",\n\t\t\tafterSHA:    \"3ba5b4456312437e8edf058b4f9967385bf14e483a0bb6d771b5881231d5c0e6\",\n\t\t\tbeforeBytes: 5967,\n\t\t\tafterBytes:  6146,\n\t\t\tbefore:      \"\\t} else {\\n\\t\\ttemplate, parseErr := r.set.FromCache(name)\",\n\t\t\tafter:       \"\\t} else if name == \\\"web/group_detail.html\\\" {\\n\\t\\traw, renderErr := r.renderGroupDetail(request, defaults)\\n\\t\\tif renderErr != nil {\\n\\t\\t\\treturn renderErr\\n\\t\\t}\\n\\t\\t_, _ = output.Write(raw)\\n\\t} else {\\n\\t\\ttemplate, parseErr := r.set.FromCache(name)\",\n\t\t},\n")) != 1 {
		return nil, fmt.Errorf("group-detail-complete-walker")
	}
	before := bytes.Replace(append([]byte(nil), raw...), []byte("\t\t// ADR-0063: exact whole group-detail dispatch; retain the complete31-row preimage.\n\t\t{\n\t\t\tpath:        \"services/server/internal/web/renderer.go\",\n\t\t\tbeforeSHA:   \"6228270fd3343f7d352b79780bd9055ccbf9fe70b2d7917acdb4e0b6ffd31ed3\",\n\t\t\tafterSHA:    \"3ba5b4456312437e8edf058b4f9967385bf14e483a0bb6d771b5881231d5c0e6\",\n\t\t\tbeforeBytes: 5967,\n\t\t\tafterBytes:  6146,\n\t\t\tbefore:      \"\\t} else {\\n\\t\\ttemplate, parseErr := r.set.FromCache(name)\",\n\t\t\tafter:       \"\\t} else if name == \\\"web/group_detail.html\\\" {\\n\\t\\traw, renderErr := r.renderGroupDetail(request, defaults)\\n\\t\\tif renderErr != nil {\\n\\t\\t\\treturn renderErr\\n\\t\\t}\\n\\t\\t_, _ = output.Write(raw)\\n\\t} else {\\n\\t\\ttemplate, parseErr := r.set.FromCache(name)\",\n\t\t},\n"), nil, 1)
	if len(before) != 61574 || guiPublicHash(before) != "08a920a05fd36516a0787aa0842e2f19d60996897171a1831e06827a712b3dd2" {
		return nil, fmt.Errorf("group-detail-exact-walker-preimage")
	}
	return before, nil
}
func groupDetailGroupCustodyPreimage(t *testing.T, raw []byte, rows []guiPublicGetRepairRow) ([]byte, []guiPublicGetRepairRow) {
	t.Helper()
	previousRows := groupDetailPreviousRepairRows(guiPublicGetRepairRows())
	if previousRows == nil {
		t.Fatal("complete newest/current relation missing")
	}
	// The retained group control explicitly enters on its recovered31-row
	// afterimage. This is never returned as a physical current source tuple.
	if len(rows) == 31 && len(raw) == 5967 && guiPublicHash(raw) == "6228270fd3343f7d352b79780bd9055ccbf9fe70b2d7917acdb4e0b6ffd31ed3" {
		for i := range rows {
			if rows[i] != previousRows[i] {
				t.Fatal("retained31-row metadata changed")
			}
		}
		return raw, rows
	}
	if groupDetailPreviousRepairRows(rows) == nil {
		t.Fatal("unsupported newest relation")
	}
	before, err := groupDetailRendererPreimage(raw)
	if err != nil {
		t.Fatal(err)
	}
	return before, previousRows
}

func TestNativeGroupDetailDispatchAndReadersRetainExactPriorAuthorities(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	path := "services/server/internal/web/renderer.go"
	raw, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	previous, err := groupDetailRendererPreimage(raw)
	if err != nil || len(previous) != 5967 || guiPublicHash(previous) != "6228270fd3343f7d352b79780bd9055ccbf9fe70b2d7917acdb4e0b6ffd31ed3" {
		t.Fatal("complete31-row renderer afterimage not recovered", err)
	}
	rows := guiPublicGetRepairRows()
	if len(rows) != 32 || len(groupDetailPreviousRepairRows(rows)) != 31 || len(groupCreatePreviousRepairRows()) != 27 {
		t.Fatal("new,retained-current,or original mutation scopes changed")
	}
	historical, err := guiPublicGetRowsPreimage(path, raw)
	if err != nil || guiPublicHash(historical) != "94f11a3f1849c919ba100c4ba68236263f71f07741006c1992dadc4020723e68" {
		t.Fatal("newest inverse lost an original relation", err)
	}
	for name, changed := range map[string][]byte{"missing-dispatch": bytes.Replace(raw, []byte(rows[0].after), nil, 1), "duplicate-dispatch": append(append([]byte(nil), raw...), []byte(rows[0].after)...), "wrong-context": bytes.Replace(raw, []byte("r.renderGroupDetail(request, defaults)"), []byte("r.renderGroupDetail(request, data)"), 1), "unrelated-byte": append(append([]byte(nil), raw...), '\n'), "old31-is-not-current": previous} {
		t.Run(name, func(t *testing.T) {
			if _, err := guiPublicGetRowsPreimage(path, changed); err == nil {
				t.Fatal("unsupported current source admitted")
			}
			if got, err := guiPublicGetRowsPreimage(path, raw); err != nil || !bytes.Equal(got, historical) {
				t.Fatal("probe changed current custody", err)
			}
		})
	}
	walker, err := os.ReadFile(filepath.Join(root, "services/server/internal/web/gui_public_get_walk_test.go"))
	oldWalker, proofErr := groupDetailWalkerPreimage(walker)
	if err != nil || proofErr != nil || guiPublicHash(oldWalker) != "08a920a05fd36516a0787aa0842e2f19d60996897171a1831e06827a712b3dd2" {
		t.Fatal("complete prior walker not recovered", err, proofErr)
	}
	for _, changed := range [][]byte{append(append([]byte(nil), walker...), '\n'), walker[:len(walker)-1], oldWalker} {
		if _, err := groupDetailWalkerPreimage(changed); err == nil {
			t.Fatal("unknown,rehashed,or prior walker admitted as current")
		}
	}
	var registry guiPreferenceGETRegistry
	if guiG1StrictJSON(guiPreferencesGETRegistryBytes, &registry) != nil {
		t.Fatal("immutable preference registry unavailable")
	}
	actual, err := guiPreferenceGETInputs(root, registry)
	if err != nil || len(actual) != 138 {
		t.Fatal("all138 current physical tuples required", err)
	}
	for path, binding := range actual {
		_, physical, err := guiCurrentTransportReadBinding(root, path, 2<<20)
		if err != nil || binding != physical {
			t.Fatal("historical tuple leaked to live reader", path, err)
		}
	}
}
