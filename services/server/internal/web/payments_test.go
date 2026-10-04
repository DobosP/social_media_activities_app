package web

import (
	"context"
	"flag"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/donations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/flosch/pongo2/v6"
)

var webDomainDSN = flag.String("web-domain-test-dsn", "", "explicit disposable native web fixture database")

func TestLegacyDonationDecimalAmountAndCampaignForm(t *testing.T) {
	for raw, want := range map[string]int64{"1": 100, "1.99": 199, " +0010.50 ": 1050, "999999.99": 99999999, "1e2": 10000, "10000000e-2": 10000000} {
		if got, err := donationCents(raw); err != nil || got != want {
			t.Fatal("decimal conversion", raw, got, err)
		}
	}
	for _, raw := range []string{"", ".50", "0", "0.99", "-1", "1.001", "1000000", "NaN", "1/2", "1.00<script>", "1,00"} {
		if _, err := donationCents(raw); err == nil {
			t.Fatal("invalid form accepted", raw)
		}
	}
	choices := []map[string]any{{"id": int64(7), "slug": "safe", "title": "<script>Campaign</script>"}}
	r := httptest.NewRequest("GET", "/donate/?campaign=safe", nil)
	form := donateForm(r, choices, "")["as_p"].(*pongo2.Value).String()
	if !strings.Contains(form, `value="7" selected`) || strings.Contains(form, "<script>") || !strings.Contains(form, "General fund") {
		t.Fatal("campaign form selection/escaping", form)
	}
	r = httptest.NewRequest("POST", "/donate/", nil)
	r.PostForm = url.Values{"amount": []string{"1.005"}, "campaign": []string{"7"}}
	form = donateForm(r, choices, "Invalid decimal")["as_p"].(*pongo2.Value).String()
	if !strings.Contains(form, "Invalid decimal") || !strings.Contains(form, `value="1.005"`) || !strings.Contains(form, `value="7" selected`) {
		t.Fatal("invalid form lost donor input")
	}
}

func TestNativeFinancialPagesRenderOriginalReceiptsAndCloseouts(t *testing.T) {
	if *webDomainDSN == "" {
		t.Skip("explicit web fixture DSN required")
	}
	db := testdb.New(t, *webDomainDSN, nil)
	ctx := context.Background()
	a := testdb.Actor(t, db, "web-own-donor", "adult")
	var campaign int64
	if err := db.QueryRow(ctx, `INSERT INTO donations_campaign(title,slug,description,goal_cents,currency,is_active,outcome,closed_at,partner_id,created) VALUES('Reading season','reading','','1000','EUR',false,'Purchased books.',now(),NULL,now()) RETURNING id`).Scan(&campaign); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO donations_donation(donor_id,amount_cents,currency,recurring,campaign_id,provider,status,external_ref,created_at,completed_at) VALUES($1,199,'EUR',true,$2,'dev','completed','opaque-own-reference',now(),now())`, a.ID, campaign); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO donations_spendentry(category,amount_cents,currency,period,note,campaign_id,created_at) VALUES('Books',199,'EUR','2026','',$1,now())`, campaign); err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../../../../")
	s := &Server{DB: db, Donations: donations.New(db, donations.Config{}), Renderer: NewRenderer(root)}
	for _, name := range []string{"my_donations", "campaigns", "transparency", "donate", "partners"} {
		t.Run(name, func(t *testing.T) {
			r := platform.WithActor(httptest.NewRequest("GET", "/"+name+"/", nil), a)
			data, template, err := s.paymentView(r, a, name)
			if err != nil {
				t.Fatal(err)
			}
			data["csrf"] = "synthetic-csrf"
			w := httptest.NewRecorder()
			if err := s.Renderer.Render(w, r, template, data); err != nil {
				t.Fatal(err)
			}
			html := w.Body.String()
			if name == "my_donations" {
				for _, want := range []string{"1.99 EUR", "Completed", "Reading season", "opaque-own-reference", "recurring"} {
					if !strings.Contains(html, want) {
						t.Fatal("missing legacy receipt", want)
					}
				}
			} else if strings.Contains(html, "opaque-own-reference") {
				t.Fatal("private receipt in public page")
			}
			if name == "campaigns" && (!strings.Contains(html, "Purchased books.") || strings.Contains(html, "progressbar") || strings.Contains(html, "aria-valuenow")) {
				t.Fatal("neutral closeout changed")
			}
			if name == "transparency" && (!strings.Contains(html, "1.99") || !strings.Contains(html, "Books")) {
				t.Fatal("cash ledger context missing")
			}
		})
	}
}
