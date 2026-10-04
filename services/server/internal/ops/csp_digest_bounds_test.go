package ops

import (
	"strings"
	"testing"
)

func TestCSPDigestArrayAndJSONLReportBounds(t *testing.T) {
	for _, payload := range []string{"[" + strings.Repeat("{},", 100000) + "{}]", strings.Repeat("{}\n", 100001)} {
		if _, err := ReadCSPDigest(strings.NewReader(payload)); err == nil {
			t.Fatal("CSP digest report cap bypassed")
		}
	}
	if _, err := ReadCSPDigest(strings.NewReader(strings.Repeat(" ", 16<<20+1))); err == nil {
		t.Fatal("CSP digest byte cap bypassed")
	}
}
