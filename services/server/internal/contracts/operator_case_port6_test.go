package contracts

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This is a source-integrity obligation, not a claim about native HTTP paging
// equivalence. The retained generated reference is read as data only.
func TestOperatorCase6FrozenGeneratedClientBodyMatchesEmbeddedStamp(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "apps/ingestion/sources/_roedu_client_core.py"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 2<<20 {
		t.Fatal("generated reference unexpectedly exceeded integrity bound")
	}
	text := string(raw)
	begin := "# --- BEGIN VENDORED romania_scraper/dataapi/roedu_client.py ---"
	end := "# --- END VENDORED ---"
	parts := strings.Split(text, begin)
	if len(parts) != 2 {
		t.Fatal("generated begin marker changed")
	}
	parts = strings.Split(parts[1], end)
	if len(parts) != 2 {
		t.Fatal("generated end marker changed")
	}
	body := strings.Trim(parts[0], "\n") + "\n"
	stamp := regexp.MustCompile(`VENDORED_SHA256 = "([0-9a-f]{64})"`).FindStringSubmatch(text)
	if len(stamp) != 2 {
		t.Fatal("generated body stamp missing")
	}
	sum := sha256.Sum256([]byte(body))
	if hex.EncodeToString(sum[:]) != stamp[1] {
		t.Fatal("retained generated reference was locally hand edited")
	}
}

func TestOperatorCase6ExportAndIndependentSidecarSourceExcludePrivateModelTypes(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	files := []string{filepath.Join(root, "services/server/internal/export/snapshot.go"), filepath.Join(root, "services/server/internal/jobs/snapshot.go")}
	readerFiles, err := filepath.Glob(filepath.Join(root, "services/agentapi/*.go"))
	if err != nil || len(readerFiles) == 0 {
		t.Fatal("independent native sidecar sources absent", err)
	}
	files = append(files, readerFiles...)
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, model := range []string{"PostReaction", "PostDissent", "PostConcern", "PostSentimentFooter"} {
			if strings.Contains(string(raw), model) {
				t.Fatal("private reaction/safety model entered public export or sidecar source", filepath.Base(file))
			}
		}
	}
}
