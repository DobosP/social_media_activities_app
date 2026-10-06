package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/donations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

const f42PartnerCredit = "In partnership with"
const f42PartnerName = "Synthetic F42 library"
const f42PartnerBlurb = "Synthetic Saturday reading hour"
const f42CampaignTitle = "Synthetic F42 reading campaign"

// Reuse payments_test.go's explicit disposable fixture flag and production
// renderer. The tests exercise only the existing anonymous /campaigns/ route;
// no provider, donor identity, external request or finance REST route is needed.
func f42PartnerFinanceHTMLFixture(t *testing.T) (*Server, *http.ServeMux, int64, int64) {
	t.Helper()
	if *webDomainDSN == "" {
		t.Skip("explicit web fixture DSN required")
	}
	db := testdb.New(t, *webDomainDSN, nil)
	auth, err := authcore.New(authcore.Config{PublicURL: "https://fixture.local"}, accounts.NewStore(db))
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../../../")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{DB: db, Auth: auth, Donations: donations.New(db, donations.Config{}), Renderer: NewRenderer(root), Config: Config{Root: root, PublicURL: "https://fixture.local"}}
	ctx := context.Background()
	var partner, campaign int64
	if err := db.QueryRow(ctx, `INSERT INTO places_partner(name,kind,blurb,place_id,website,is_verified,is_active,created_at,updated_at) VALUES($1,'library',$2,NULL,'https://ok.example',true,true,now(),now()) RETURNING id`, f42PartnerName, f42PartnerBlurb).Scan(&partner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO donations_campaign(title,slug,description,goal_cents,currency,is_active,outcome,closed_at,partner_id,created) VALUES($1,'f42-reading','',100000,'EUR',true,'',NULL,$2,now()) RETURNING id`, f42CampaignTitle, partner).Scan(&campaign); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	return s, mux, partner, campaign
}

func f42PartnerCampaignRow(t *testing.T, s *Server) map[string]any {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "https://fixture.local/campaigns/", nil)
	data, template, err := s.paymentView(r, platform.Actor{}, "campaigns")
	if err != nil || template != "web/campaigns.html" {
		t.Fatal("campaign payment view", template, err)
	}
	rows, ok := data["campaigns"].([]map[string]any)
	if !ok || len(rows) != 1 || rows[0]["title"] != f42CampaignTitle {
		t.Fatal("active linked campaign must remain in the payment view", data["campaigns"])
	}
	return rows[0]
}

// Frozen: apps/donations/tests/test_f42_partner_campaign.py::test_malicious_partner_website_is_never_a_live_link
// A stored unsafe URL must reach the real template's safe_href filter, while
// the partner's text credit and the campaign association survive.
func TestCasePortF42MaliciousPartnerWebsiteNeverLiveLink(t *testing.T) {
	s, mux, partner, campaign := f42PartnerFinanceHTMLFixture(t)
	ctx := context.Background()
	baseline := webCasePortHTML(t, mux, platform.Actor{}, "/campaigns/")
	webCasePortContains(t, baseline, f42CampaignTitle, f42PartnerCredit, f42PartnerName, `href="https://ok.example"`)
	if _, err := s.DB.Exec(ctx, `UPDATE places_partner SET website='javascript:alert(1)' WHERE id=$1`, partner); err != nil {
		t.Fatal(err)
	}
	row := f42PartnerCampaignRow(t, s)
	if row["partner_website"] != "javascript:alert(1)" || row["partner_name"] != f42PartnerName {
		t.Fatal("fixture must exercise render-time URL sanitization with a retained credit", row)
	}
	body := webCasePortHTML(t, mux, platform.Actor{}, "/campaigns/")
	webCasePortContains(t, body, f42CampaignTitle, f42PartnerCredit, f42PartnerName)
	webCasePortAbsent(t, body, "javascript:", `href="javascript:alert(1)"`, `href="https://ok.example"`)
	// Preserve the original association sanity check, and prove rendering did
	// not hide or rewrite the malicious fixture in storage.
	var linked int64
	var website string
	if err := s.DB.QueryRow(ctx, `SELECT c.partner_id,p.website FROM donations_campaign c JOIN places_partner p ON p.id=c.partner_id WHERE c.id=$1`, campaign).Scan(&linked, &website); err != nil {
		t.Fatal(err)
	}
	if linked != partner || website != "javascript:alert(1)" {
		t.Fatal("campaign credit association or forced URL changed", linked, website)
	}
}

// Frozen: apps/donations/tests/test_f42_partner_campaign.py::test_non_public_partner_is_not_credited
// Exercise all three original flag combinations after a successful public
// request, so neither a dropped campaign nor a cached credit can pass.
func TestCasePortF42NonPublicPartnerNeverCredited(t *testing.T) {
	for _, tc := range []struct {
		name             string
		verified, active bool
	}{
		{"unverified_active", false, true},
		{"verified_inactive", true, false},
		{"unverified_inactive", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mux, partner, campaign := f42PartnerFinanceHTMLFixture(t)
			ctx := context.Background()
			baseline := webCasePortHTML(t, mux, platform.Actor{}, "/campaigns/")
			webCasePortContains(t, baseline, f42CampaignTitle, f42PartnerCredit, f42PartnerName, f42PartnerBlurb)
			if _, err := s.DB.Exec(ctx, `UPDATE places_partner SET is_verified=$2,is_active=$3 WHERE id=$1`, partner, tc.verified, tc.active); err != nil {
				t.Fatal(err)
			}
			row := f42PartnerCampaignRow(t, s)
			for _, field := range []string{"partner_name", "partner_blurb", "partner_website"} {
				if row[field] != "" {
					t.Fatal("nonpublic partner credit survived read-time gate", tc.name, field, row[field])
				}
			}
			body := webCasePortHTML(t, mux, platform.Actor{}, "/campaigns/")
			webCasePortContains(t, body, f42CampaignTitle)
			webCasePortAbsent(t, body, f42PartnerCredit, f42PartnerName, f42PartnerBlurb, "https://ok.example")
			var linked int64
			var verified, active, campaignActive bool
			if err := s.DB.QueryRow(ctx, `SELECT c.partner_id,c.is_active,p.is_verified,p.is_active FROM donations_campaign c JOIN places_partner p ON p.id=c.partner_id WHERE c.id=$1`, campaign).Scan(&linked, &campaignActive, &verified, &active); err != nil {
				t.Fatal(err)
			}
			if linked != partner || !campaignActive || verified != tc.verified || active != tc.active {
				t.Fatal("read-time credit gate changed the fixture association or flags", linked, campaignActive, verified, active)
			}
		})
	}
}
