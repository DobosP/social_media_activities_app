package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// These benchmarks execute the assembled native server over a disposable
// database. They measure this fixture/machine only, never hosted cost or a
// production throughput promise. Fixed request counts stay within admission.
func BenchmarkNativeAnonymousContractPaths(b *testing.B) {
	for _, path := range []string{"/healthz", "/readyz", "/api/v1/events/?limit=20", "/api/v1/places/?page_size=20", "/api/schema/"} {
		b.Run(path, func(b *testing.B) {
			if b.N > 50 {
				b.Fatal("use -benchtime=20x for the bounded synthetic admission benchmark")
			}
			db := testdb.New(b, *appDSN, nil)
			application, err := New(context.Background(), db, integrationConfig(b), true)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodGet, "https://app.example"+path, nil)
				application.ServeHTTP(response, request)
				if response.Code != http.StatusOK || response.Header().Get("X-Social-Runtime") != "go" {
					b.Fatalf("native fixture response status=%d", response.Code)
				}
			}
		})
	}
}
