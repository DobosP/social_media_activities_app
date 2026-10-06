package donations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

type paymentTransport func(*http.Request) (*http.Response, error)

func (f paymentTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failedPayment struct{}

func (failedPayment) Name() string { return "fixture" }
func (failedPayment) CreateIntent(context.Context, int64, string, string) (string, string, error) {
	return "", "", errors.New("fixture unavailable")
}

func TestNativeStripeCheckoutProtocolAndSafeRetry(t *testing.T) {
	calls := 0
	s := New(nil, Config{Provider: "apps.donations.providers.StripePaymentProvider", StripeSecret: "synthetic-stripe", SuccessURL: "https://fixture.invalid/thanks", CancelURL: "https://fixture.invalid/donate", Client: &http.Client{Transport: paymentTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://api.stripe.com/v1/checkout/sessions" || r.Header.Get("Idempotency-Key") != "opaque-reference" {
			t.Fatal("checkout identity/protocol drift")
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "synthetic-stripe" || password != "" {
			t.Fatal("provider authentication")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		for name, want := range map[string]string{"mode": "payment", "line_items[0][price_data][unit_amount]": "199", "line_items[0][price_data][currency]": "eur", "client_reference_id": "opaque-reference"} {
			if r.Form.Get(name) != want {
				t.Fatal("checkout form", name)
			}
		}
		status, body := 200, `{"id":"cs_fixture","url":"https://checkout.stripe.com/c/fixture"}`
		if calls == 1 {
			status, body = 503, `{}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}})
	checkout, ref, err := s.intent(context.Background(), "opaque-reference", 199, "EUR")
	if err != nil || calls != 2 || ref != "cs_fixture" || checkout != "https://checkout.stripe.com/c/fixture" {
		t.Fatal("safe checkout retry", calls, err)
	}
	for _, url := range []string{"http://fixture.invalid", "https://user:pass@fixture.invalid", "https://fixture.invalid\\escape", "javascript:alert(1)"} {
		if safeURL(url) {
			t.Fatal("unsafe checkout URL", url)
		}
	}
}

func TestNativeFinancialLedgerSemanticsAndOwnReceipts(t *testing.T) {
	if *domainDSN == "" {
		t.Skip("explicit fixture DSN required")
	}
	db := testdb.New(t, *domainDSN, nil)
	ctx := context.Background()
	donor := testdb.Actor(t, db, "own-financial-receipt", "adult")
	other := testdb.Actor(t, db, "foreign-financial-receipt", "adult")
	place := testdb.Place(t, db, "Stewarded fixture venue", "osm")
	var partner int64
	if err := db.QueryRow(ctx, `INSERT INTO places_partner(name,kind,blurb,place_id,website,is_verified,is_active,created_at,updated_at) VALUES('Fixture library','library','Public acknowledgement',$1,'https://fixture.invalid',true,true,now(),now()) RETURNING id`, place).Scan(&partner); err != nil {
		t.Fatal(err)
	}
	campaign := func(slug, outcome string, closed bool) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRow(ctx, `INSERT INTO donations_campaign(title,slug,description,goal_cents,currency,is_active,outcome,closed_at,partner_id,created) VALUES($1,$1,'Plain mission',1000,'EUR',true,$2,CASE WHEN $3 THEN now() ELSE NULL END,$4,now()) RETURNING id`, slug, outcome, closed, partner).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	active := campaign("active", "", false)
	done := campaign("completed", "Funded a season of reading.", true)
	campaign("blank-closeout", " \n\t\u00a0 ", true)
	for _, gift := range []struct {
		donor, amount, campaign int64
		currency, status, ref   string
	}{{donor.ID, 1500, active, "EUR", "completed", "own-reference"}, {donor.ID, 250, done, "EUR", "completed", "done-reference"}, {other.ID, 9999, active, "EUR", "pending", "private-foreign-reference"}, {other.ID, 100, active, "RON", "completed", "other-reference"}} {
		if _, err := db.Exec(ctx, `INSERT INTO donations_donation(donor_id,amount_cents,currency,recurring,campaign_id,provider,status,external_ref,created_at,completed_at) VALUES($1,$2,$3,false,$4,'dev',$5::text,$6,now(),CASE WHEN $5::text='completed' THEN now() ELSE NULL END)`, gift.donor, gift.amount, gift.currency, gift.campaign, gift.status, gift.ref); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO donations_spendentry(category,amount_cents,currency,period,note,campaign_id,created_at) VALUES('Books',200,'EUR','2026','private staff note',$1,now()),('General',50,'EUR','','',NULL,now()),('Other currency',900,'RON','','',NULL,now())`, done); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO donations_inkindcontribution(category,quantity,unit_text,value_cents,currency,period,note,partner_id,created_at) VALUES('Library',2,'room-hours',500,'EUR','','private credit',$1,now()),('Library',3,'room-hours',NULL,'EUR','','',$1,now()),('Library',7,'kits',NULL,'EUR','','',$1,now())`, partner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO donations_civicoutcome(headline,detail,period,partner_id,is_active,created_at) VALUES('Staff-authored outcome','Independent prose','2026',$1,true,now()),('Retired prose','','',NULL,false,now())`, partner); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 8; i++ {
		if _, err := db.Exec(ctx, `INSERT INTO donations_costanchor(label,amount_cents,currency,spend_category,is_active,created_at) VALUES($1,$2,'EUR','Illustrative',true,now())`, fmt.Sprint("Anchor ", i), i*100); err != nil {
			t.Fatal(err)
		}
	}
	s := New(db, Config{})
	ledger, err := s.Ledger(ctx, "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if ledger["total_cents"] != int64(1750) || ledger["spent_cents"] != int64(250) {
		t.Fatal("cash/in-kind/currency categories merged")
	}
	anchors := ledger["cost_anchors"].([]map[string]any)
	if len(anchors) != 6 || anchors[0]["amount_cents"] != int64(800) {
		t.Fatal("cost anchor order/cap")
	}
	closed := ledger["completed_campaigns"].([]map[string]any)
	if len(closed) != 1 || closed[0]["slug"] != "completed" || len(closed[0]["spend_entries"].([]any)) != 1 {
		t.Fatal("closeout publication boundary")
	}
	linkedSpend := closed[0]["spend_entries"].([]any)[0].(map[string]any)
	if linkedSpend["category"] != "Books" {
		t.Fatal("closeout linked spend category/untagged exclusion", linkedSpend)
	}
	if _, ok := closed[0]["goal_cents"]; ok {
		t.Fatal("closeout goal/vanity metric")
	}
	kinds := ledger["in_kind"].([]map[string]any)
	if len(kinds) != 2 || kinds[1]["total_quantity"] != int64(5) {
		t.Fatal("incompatible in-kind units merged", kinds)
	}
	partners := ledger["partners"].([]map[string]any)
	if len(partners) != 1 || partners[0]["get_kind_display"] != "Library" || partners[0]["place"].(map[string]any)["pk"] != place {
		t.Fatal("partner acknowledgement lost")
	}
	outcomes := ledger["civic_outcomes"].([]map[string]any)
	if len(outcomes) != 1 || outcomes[0]["partner_name"] != "Fixture library" {
		t.Fatal("public civic partner acknowledgement lost", outcomes)
	}
	raw, _ := json.Marshal(ledger)
	for _, private := range []string{"own-reference", "private-foreign-reference", "private staff note", "private credit", "donor_id", "Retired prose"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("public financial identity leakage", private)
		}
	}
	mine, err := s.Mine(ctx, donor)
	if err != nil || len(mine) != 2 {
		t.Fatal("own receipts", err)
	}
	raw, _ = json.Marshal(mine)
	if !strings.Contains(string(raw), "own-reference") || strings.Contains(string(raw), "private-foreign-reference") {
		t.Fatal("own receipt boundary")
	}
	if _, err := db.Exec(ctx, `UPDATE places_partner SET is_verified=false WHERE id=$1`, partner); err != nil {
		t.Fatal(err)
	}
	ledger, err = s.Ledger(ctx, "EUR")
	if err != nil {
		t.Fatal(err)
	}
	outcomes = ledger["civic_outcomes"].([]map[string]any)
	if len(outcomes) != 1 || outcomes[0]["headline"] != "Staff-authored outcome" {
		t.Fatal("unverified partner removed civic outcome prose", outcomes)
	}
	if name, ok := outcomes[0]["partner_name"]; !ok || name != nil {
		t.Fatal("unverified civic partner credit at read time", outcomes)
	}
	if _, err := db.Exec(ctx, `UPDATE places_partner SET is_verified=true WHERE id=$1`, partner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE places_partner SET is_active=false WHERE id=$1`, partner); err != nil {
		t.Fatal(err)
	}
	ledger, err = s.Ledger(ctx, "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger["partners"].([]map[string]any)) != 0 || ledger["campaigns"].([]map[string]any)[0]["partner_name"] != "" || ledger["civic_outcomes"].([]map[string]any)[0]["partner_name"] != nil {
		t.Fatal("stale partner credit")
	}
	zero := 0
	s.Config.CostAnchorsMax = &zero
	ledger, err = s.Ledger(ctx, "EUR")
	if err != nil || len(ledger["cost_anchors"].([]map[string]any)) != 0 {
		t.Fatal("disabled illustrative anchors")
	}
	s.Config.Adapter = failedPayment{}
	s.Config.Provider = "fixture"
	if _, err := s.Start(ctx, platform.Actor{}, Request{Amount: 500}); !errors.Is(err, ErrProvider) {
		t.Fatal("provider failure mapping", err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM donations_donation`).Scan(&count); err != nil || count != 4 {
		t.Fatal("failed intent persisted", err, count)
	}
}

func TestNativeStripePaidCompletionAndReplay(t *testing.T) {
	if *domainDSN == "" {
		t.Skip("explicit fixture DSN required")
	}
	db := testdb.New(t, *domainDSN, nil)
	ctx := context.Background()
	now := time.Unix(1760000000, 0)
	if _, err := db.Exec(ctx, `INSERT INTO donations_donation(donor_id,amount_cents,currency,recurring,campaign_id,provider,status,external_ref,created_at,completed_at) VALUES(NULL,500,'EUR',false,NULL,'stripe','pending','cs_fixture',now(),NULL)`); err != nil {
		t.Fatal(err)
	}
	s := New(db, Config{Provider: "stripe", StripeWebhookSecret: "synthetic-signature", Now: func() time.Time { return now }})
	mux := http.NewServeMux()
	s.Register(mux)
	call := func(status string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":"cs_fixture","payment_status":%q}}}`, status)
		mac := hmac.New(sha256.New, []byte("synthetic-signature"))
		mac.Write([]byte("1760000000." + body))
		r := httptest.NewRequest("POST", "/api/v1/donations/webhook/", strings.NewReader(body))
		r.Header.Set("Stripe-Signature", "t=1760000000, v1="+hex.EncodeToString(mac.Sum(nil)))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if got := call("unpaid"); got.Code != 200 || !strings.Contains(got.Body.String(), `"status":"ignored"`) {
		t.Fatal("unpaid checkout marked complete", got.Body.String())
	}
	if got := call("paid"); got.Code != 200 || !strings.Contains(got.Body.String(), `"status":"completed"`) {
		t.Fatal("paid checkout rejected", got.Body.String())
	}
	if got := call("paid"); !strings.Contains(got.Body.String(), `"status":"ignored"`) {
		t.Fatal("payment completed twice")
	}
}
