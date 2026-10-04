package avatars

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestPythonByteParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/python-goldens.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Rows []struct {
			Seed           string
			Nodes          []Node
			Edges          []Edge
			Generation, PX int
			Intensity      float64
			UID, SHA256    string
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for i, row := range fixture.Rows {
		svg := RenderGeneration(row.Generation, row.Seed, row.Nodes, row.Edges, Options{PX: row.PX, Intensity: row.Intensity, UIDOverride: row.UID})
		actual := fmt.Sprintf("%x", sha256.Sum256([]byte(svg)))
		if actual != row.SHA256 {
			t.Fatalf("Python byte parity row%d gen%d px%d seed%q intensity%g: got%s want%s", i, row.Generation, row.PX, row.Seed, row.Intensity, actual, row.SHA256)
		}
	}
}

func TestPythonActivityAccentByteParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/python-accents.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Seed          string
		Width, Height int
		SHA256        string
	}
	if err = json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		svg := ActivityAccentWithSize(row.Seed, row.Width, row.Height)
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(svg)))
		if hash != row.SHA256 {
			t.Fatal("activity accent parity", i, hash, row.SHA256)
		}
	}
}
