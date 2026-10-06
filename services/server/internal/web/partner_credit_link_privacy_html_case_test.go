package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/donations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

const f42CreditLinkPartner = "Synthetic F42 credit library"
const f42CreditLinkBlurb = "Synthetic civic reading support"
const f42CreditLinkCampaign = "Synthetic F42 credit privacy reading"

// This batch stands alone from the separately prepared malicious/nonpublic
// partner cases. Reuse payments_test.go's explicit disposable DSN and unchanged
// renderer, exercising only the existing anonymous /campaigns/ HTML route.
func f42CreditLinkPrivacyFixture(t *testing.T, website string) (*Server, *http.ServeMux, int64) {
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
	if err := db.QueryRow(ctx, `INSERT INTO places_partner(name,kind,blurb,place_id,website,is_verified,is_active,created_at,updated_at) VALUES($1,'library',$2,NULL,$3,true,true,now(),now()) RETURNING id`, f42CreditLinkPartner, f42CreditLinkBlurb, website).Scan(&partner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO donations_campaign(title,slug,description,goal_cents,currency,is_active,outcome,closed_at,partner_id,created) VALUES($1,'f42-credit-privacy','',100000,'EUR',true,'',NULL,$2,now()) RETURNING id`, f42CreditLinkCampaign, partner).Scan(&campaign); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	return s, mux, campaign
}

func f42CreditLinkCampaignRow(t *testing.T, s *Server) map[string]any {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "https://fixture.local/campaigns/", nil)
	data, template, err := s.paymentView(r, platform.Actor{}, "campaigns")
	if err != nil || template != "web/campaigns.html" {
		t.Fatal("campaign payment view", template, err)
	}
	rows, ok := data["campaigns"].([]map[string]any)
	if !ok || len(rows) != 1 || rows[0]["title"] != f42CreditLinkCampaign || rows[0]["partner_name"] != f42CreditLinkPartner {
		t.Fatal("active public partner campaign must remain in payment view", data["campaigns"])
	}
	return rows[0]
}

// Frozen: apps/donations/tests/test_f42_partner_campaign.py::test_credit_does_not_expose_donor_pii
// Assert the actual rendered credit and both original donor-identity exclusions
// after independently checking the linked completed donation and its identities.
func TestCasePortF42CampaignPartnerCreditExcludesDonorPII(t *testing.T) {
	s, mux, campaign := f42CreditLinkPrivacyFixture(t, "")
	ctx := context.Background()
	donor := testdb.Actor(t, s.DB, "secretdonor", "adult")
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET display_name='Donor X' WHERE id=$1`, donor.ID); err != nil {
		t.Fatal(err)
	}
	var donation int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO donations_donation(donor_id,amount_cents,currency,recurring,campaign_id,provider,status,external_ref,created_at,completed_at) VALUES($1,5000,'EUR',false,$2,'dev','completed','f42-ref',now(),now()) RETURNING id`, donor.ID, campaign).Scan(&donation); err != nil {
		t.Fatal(err)
	}
	var username, displayName, status string
	var linkedCampaign, amount int64
	if err := s.DB.QueryRow(ctx, `SELECT u.username,u.display_name,d.campaign_id,d.amount_cents,d.status FROM donations_donation d JOIN accounts_user u ON u.id=d.donor_id WHERE d.id=$1 AND d.donor_id=$2`, donation, donor.ID).Scan(&username, &displayName, &linkedCampaign, &amount, &status); err != nil {
		t.Fatal(err)
	}
	if username != "secretdonor" || displayName != "Donor X" || linkedCampaign != campaign || amount != 5000 || status != "completed" {
		t.Fatal("donor privacy fixture must retain original identities and linked completed gift", username, displayName, linkedCampaign, amount, status)
	}
	row := f42CreditLinkCampaignRow(t, s)
	if row["raised_cents"] != int64(5000) {
		t.Fatal("completed linked gift must contribute to rendered campaign", row["raised_cents"])
	}
	body := webCasePortHTML(t, mux, platform.Actor{}, "/campaigns/")
	webCasePortContains(t, body, f42CreditLinkCampaign, "In partnership with", f42CreditLinkPartner, f42CreditLinkBlurb, "Raised 50.00 of 1000.00 EUR goal.")
	webCasePortAbsent(t, body, username, displayName)
}

// Frozen: apps/donations/tests/test_f42_partner_campaign.py::test_partner_website_renders_as_a_sanitised_link
// The exact original URL must survive the service projection and the real
// template's safe_href filter as a hyperlink attached to the credited partner.
func TestCasePortF42CampaignPartnerWebsiteSanitizedLink(t *testing.T) {
	s, mux, _ := f42CreditLinkPrivacyFixture(t, "https://example.org")
	row := f42CreditLinkCampaignRow(t, s)
	if row["partner_website"] != "https://example.org" {
		t.Fatal("valid partner website missing from campaign projection", row["partner_website"])
	}
	body := webCasePortHTML(t, mux, platform.Actor{}, "/campaigns/")
	webCasePortContains(t, body, f42CreditLinkCampaign, "In partnership with", f42CreditLinkPartner, `href="https://example.org"`)
	anchor := regexp.MustCompile(`<a\b([^>]*)>` + regexp.QuoteMeta(f42CreditLinkPartner) + `</a>`).FindStringSubmatch(body)
	if len(anchor) != 2 {
		t.Fatal("credited partner website must be a rendered hyperlink")
	}
	for _, attribute := range []string{`href="https://example.org"`, `rel="noopener noreferrer"`, `target="_blank"`} {
		if !strings.Contains(anchor[1], attribute) {
			t.Fatal("credited partner hyperlink lost safe attribute", attribute)
		}
	}
}
