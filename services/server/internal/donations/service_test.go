package donations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var domainDSN = flag.String("domain-test-dsn", "", "explicit disposable native domain database")

func TestStripeSignatureProofAndReplayWindow(t *testing.T) {
	now := time.Unix(1760000000, 0)
	body := []byte(`{"type":"checkout.session.completed"}`)
	mac := hmac.New(sha256.New, []byte("synthetic-webhook"))
	mac.Write([]byte("1760000000."))
	mac.Write(body)
	header := "t=1760000000,v1=" + hex.EncodeToString(mac.Sum(nil))
	if !validStripe(body, header, "synthetic-webhook", now) {
		t.Fatal("valid proof rejected")
	}
	for _, test := range []struct {
		body           []byte
		header, secret string
		now            time.Time
	}{{append(body, ' '), header, "synthetic-webhook", now}, {body, header, "", now}, {body, header, "synthetic-webhook", now.Add(301 * time.Second)}, {body, header, "synthetic-webhook", now.Add(-301 * time.Second)}} {
		if validStripe(test.body, test.header, test.secret, test.now) {
			t.Fatal("invalid proof accepted")
		}
	}
}
func TestNativeDonationWebhookAndPublicLedger(t *testing.T) {
	if *domainDSN == "" {
		t.Skip("explicit fixture DSN required")
	}
	db := testdb.New(t, *domainDSN, nil)
	actor := testdb.Actor(t, db, "synthetic-donor", "adult")
	s := New(db, Config{Provider: "dev", WebhookSecret: "synthetic-webhook"})
	mux := http.NewServeMux()
	s.Register(mux)
	call := func(method, path, body, proof string) *httptest.ResponseRecorder {
		r := platform.WithActor(httptest.NewRequest(method, path, strings.NewReader(body)), actor)
		if proof != "" {
			r.Header.Set("X-Webhook-Secret", proof)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if call("POST", "/api/donations/", `{"amount_cents":99}`, "").Code != 400 {
		t.Fatal("minimum bypass")
	}
	response := call("POST", "/api/donations/", `{"amount_cents":250,"currency":"EUR","recurring":true}`, "")
	if response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	var record struct {
		ID          int64
		CheckoutURL string `json:"checkout_url"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &record)
	ref := strings.TrimPrefix(record.CheckoutURL, "dev://checkout/")
	body := fmt.Sprintf(`{"external_ref":%q}`, ref)
	if call("POST", "/api/donations/webhook/", body, "").Code != 403 {
		t.Fatal("unsigned completion")
	}
	response = call("POST", "/api/donations/webhook/", body, "synthetic-webhook")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"completed"`) {
		t.Fatal("completion failed", response.Code, response.Body.String())
	}
	if got := call("POST", "/api/donations/webhook/", body, "synthetic-webhook"); !strings.Contains(got.Body.String(), `"status":"ignored"`) {
		t.Fatal("repeat completed twice")
	}
	response = call("GET", "/api/donations/total/", "", "")
	if !strings.Contains(response.Body.String(), `"total_cents":250`) || strings.Contains(response.Body.String(), "donor") {
		t.Fatal("public ledger boundary")
	}
	ledger, err := s.Ledger(context.Background(), "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if ledger["total_cents"] != int64(250) {
		t.Fatal("wrong native ledger total")
	}
}
