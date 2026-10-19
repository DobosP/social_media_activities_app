package web

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeFamilyDispatchCustodyIsExactAndRefusesOtherBytes(t *testing.T) {
	const path = "services/server/internal/web/renderer.go"
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	rows := guiPublicGetRepairRows()
	if len(rows) < 2 {
		t.Fatal("previous reviewed custody rows disappeared")
	}
	row := rows[0]
	if row.path != path || row.beforeSHA != "7c1ec3a7601072ce88e2c4d39184faa9fdb734066681980ebad1ab0a04a98280" || row.beforeBytes != 5539 || guiPublicHash(raw) != row.afterSHA || len(raw) != row.afterBytes || bytes.Count(raw, []byte(row.after)) != 1 {
		t.Fatal("complete current dispatch is not the admitted afterimage")
	}
	if rows[1].path != path || rows[1].afterSHA != row.beforeSHA || rows[1].beforeSHA != "94f11a3f1849c919ba100c4ba68236263f71f07741006c1992dadc4020723e68" {
		t.Fatal("native inverse replaced the previous historical relation")
	}
	previous := bytes.Replace(append([]byte(nil), raw...), []byte(row.after), []byte(row.before), 1)
	if len(previous) != row.beforeBytes || guiPublicHash(previous) != row.beforeSHA {
		t.Fatal("new inverse did not recover the complete old renderer")
	}
	historical, err := guiPublicGetRowsPreimage(path, raw)
	if err != nil || len(historical) != 5510 || guiPublicHash(historical) != "94f11a3f1849c919ba100c4ba68236263f71f07741006c1992dadc4020723e68" {
		t.Fatal("all previous custody inverses no longer reach the exact historical source", err)
	}
	for name, changed := range map[string][]byte{
		"missing-dispatch":    bytes.Replace(raw, []byte(row.after), nil, 1),
		"duplicate-dispatch":  append(append([]byte(nil), raw...), []byte(row.after)...),
		"modified-dispatch":   bytes.Replace(raw, []byte("r.renderFamily(request, name, defaults)"), []byte("r.renderFamily(request, name, data)"), 1),
		"unrelated-byte":      append(append([]byte(nil), raw...), '\n'),
		"old-current-profile": previous,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := guiPublicGetRowsPreimage(path, changed); err == nil {
				t.Fatal("unsupported current source admitted")
			}
			if got, err := guiPublicGetRowsPreimage(path, raw); err != nil || !bytes.Equal(got, historical) {
				t.Fatal("negative control aliased the physical source", err)
			}
		})
	}
}
