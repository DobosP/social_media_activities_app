package platform

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestPythonCanonicalJSONGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/python-canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Input     json.RawMessage
		Canonical string
	}
	if err = json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		decoder := json.NewDecoder(bytes.NewReader(row.Input))
		decoder.UseNumber()
		var value any
		if err = decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		actual, err := CanonicalJSON(value)
		if err != nil || string(actual) != row.Canonical {
			t.Fatalf("canonical oracle%d got%s want%s err%v", i, actual, row.Canonical, err)
		}
	}
}
func TestCanonicalNativeFloatTypeAndNonFinite(t *testing.T) {
	for _, value := range []float64{1, 1000000, 1e-7, -0.00001} {
		actual, err := CanonicalJSON(value)
		if err != nil {
			t.Fatal(err)
		}
		if value == 1 && string(actual) != "1.0" || value == 1000000 && string(actual) != "1000000.0" || value == 1e-7 && string(actual) != "1e-07" || value == -0.00001 && string(actual) != "-1e-05" {
			t.Fatal("native float lost Python representation", string(actual))
		}
	}
	if _, err := CanonicalJSON(math.Inf(1)); err == nil {
		t.Fatal("nonfinite audit accepted")
	}
}
