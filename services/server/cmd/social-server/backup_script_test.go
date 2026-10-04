package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeBackupDeploymentPipelineUsesPrivateScratchAndNativeUpload(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "deploy", "backup.sh")
	work := t.TempDir()
	bin := filepath.Join(work, "bin")
	if err = os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write("pg_dump", "#!/bin/sh\n[ \"$1\" = --no-owner ] && [ \"$2\" = --no-privileges ] || exit 1\nprintf '%s\\n' '-- synthetic PostgreSQL dump' 'SELECT 1;'\n")
	write("native-fixture", `#!/bin/sh
set -eu
[ "$1" = --backup-upload ] && [ "$3" = --backup-key ] || exit 1
[ -f "$2" ] && [ "$(stat -c '%a' "$2")" = 600 ] || exit 1
case "$2" in "$PWD"/var/backup-work/*.sql.gz) ;; *) exit 1 ;; esac
case "$4" in backups/db/socialapp-db-????????T??????Z.sql.gz) ;; *) exit 1 ;; esac
gzip -t "$2"
printf '%s\n' "$2" > fixture-dump-path
printf '%s\n' 'native backup upload invoked'
`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/bash", script, filepath.Join(bin, "native-fixture"))
	command.Dir = work
	command.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "DATABASE_URL=postgis://synthetic.invalid/fixture", "MEDIA_S3_ENDPOINT_URL=https://eu-storage.fixture.test", "MEDIA_S3_BUCKET=fixture-private-bucket"}
	out, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "native backup upload invoked") {
		t.Fatal("native backup deployment pipeline fixture failed")
	}
	path, err := os.ReadFile(filepath.Join(work, "fixture-dump-path"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(strings.TrimSpace(string(path))); !os.IsNotExist(err) {
		t.Fatal("private dump scratch was retained after upload")
	}
	info, err := os.Stat(filepath.Join(work, "var", "backup-work"))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("backup scratch directory not private")
	}
	cloud, err := os.ReadFile(filepath.Join(root, "deploy", "cloud-init.yaml.tftpl"))
	if err != nil || strings.Contains(string(cloud), "- awscli") {
		t.Fatal("deployment still requires the retired storage CLI")
	}
}
