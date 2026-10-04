package web

import (
	"encoding/json"
	"os"
	"testing"
)

func TestThreadMarkupPythonParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/markup-python.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Body       string
		Roster     map[string]bool
		AllowLinks bool `json:"allow_links"`
		HTML       string
	}
	if json.Unmarshal(raw, &cases) != nil {
		t.Fatal("invalid oracle")
	}
	for _, c := range cases {
		if got := BodyMarkup(c.Body, c.Roster, c.AllowLinks); got != c.HTML {
			t.Errorf("markup differs for %q links=%v\n got %s\nwant %s", c.Body, c.AllowLinks, got, c.HTML)
		}
	}
}
