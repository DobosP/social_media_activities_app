package media

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRetirementManagedScannerActualHTTPVerdictMatrix(t *testing.T) {
	type response struct {
		code int
		body string
	}
	var current atomic.Value
	current.Store(response{http.StatusOK, `{"match":false}`})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Error("scanner did not use bounded POST")
		}
		value := current.Load().(response)
		w.WriteHeader(value.code)
		_, _ = io.WriteString(w, value.body)
	}))
	defer server.Close()
	scanner := ManagedScanner{Endpoint: server.URL, Client: server.Client()}
	for _, scenario := range []struct {
		name           string
		response       response
		clean, failure bool
	}{
		{"clean", response{http.StatusOK, `{"match":false}`}, true, false},
		{"match", response{http.StatusOK, `{"match":true}`}, false, false},
		{"flagged", response{http.StatusOK, `{"flagged":true}`}, false, false},
		{"malformed", response{http.StatusOK, `{`}, false, true},
		{"ambiguous-null", response{http.StatusOK, `{"match":null}`}, false, true},
		{"server-error", response{http.StatusInternalServerError, `{"match":false}`}, false, true},
		{"oversized-verdict", response{http.StatusOK, strings.Repeat(" ", 4097)}, false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			current.Store(scenario.response)
			verdict, err := scanner.Scan(context.Background(), ScanInput{SHA256: strings.Repeat("a", 64)})
			if scenario.failure {
				if !errors.Is(err, ErrScanner) || verdict.Clean {
					t.Fatal("failed provider response silently admitted image")
				}
			} else if err != nil || verdict.Clean != scenario.clean {
				t.Fatal("explicit provider match/clean verdict not honored", err)
			}
		})
	}
	if verdict, err := (ManagedScanner{}).Scan(context.Background(), ScanInput{SHA256: strings.Repeat("a", 64)}); !errors.Is(err, ErrScanner) || verdict.Clean {
		t.Fatal("unconfigured managed scanner admitted image")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if verdict, err := scanner.Scan(canceled, ScanInput{SHA256: strings.Repeat("a", 64)}); !errors.Is(err, ErrScanner) || verdict.Clean {
		t.Fatal("canceled provider operation silently admitted image")
	}
}
