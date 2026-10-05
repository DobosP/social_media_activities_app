package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestPostgresOperatorCase6ActualRenameOrderAndManifestLastHashes(t *testing.T) {
	r := jobFixture(t)
	testdb.Place(t, r.DB, "Bibliotecă", "osm")
	dir := t.TempDir()
	r.Config.AgentSnapshotDir = dir
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	if _, err := syscall.InotifyAddWatch(fd, dir, syscall.IN_MOVED_TO); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), "export_agent_snapshot", nil); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 65536)
	n, err := syscall.Read(fd, buffer)
	if err != nil {
		t.Fatal("actual rename event stream", err)
	}
	names := []string{}
	for offset := 0; offset+16 <= n; {
		length := int(binary.NativeEndian.Uint32(buffer[offset+12 : offset+16]))
		if offset+16+length > n {
			t.Fatal("partial rename event")
		}
		mask := binary.NativeEndian.Uint32(buffer[offset+4 : offset+8])
		if mask&syscall.IN_MOVED_TO != 0 {
			names = append(names, strings.TrimRight(string(buffer[offset+16:offset+16+length]), "\x00"))
		}
		offset += 16 + length
	}
	if !reflect.DeepEqual(names, []string{"events.json", "places.json", "activities.json", "taxonomy.json", "manifest.json"}) {
		t.Fatal("actual filesystem publication order differs from source", names)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Generated string `json:"generated_at"`
		Datasets  map[string]struct {
			File string `json:"file"`
			SHA  string `json:"sha256"`
		} `json:"datasets"`
	}
	if json.Unmarshal(raw, &manifest) != nil || len(manifest.Datasets) != 4 {
		t.Fatal("invalid published manifest")
	}
	for _, dataset := range manifest.Datasets {
		data, err := os.ReadFile(filepath.Join(dir, dataset.File))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		var payload struct {
			Generated string `json:"generated_at"`
		}
		if json.Unmarshal(data, &payload) != nil || dataset.SHA != hex.EncodeToString(hash[:]) || payload.Generated != manifest.Generated {
			t.Fatal("manifest-last publication does not bind exact existing dataset bytes/generation")
		}
	}
}
