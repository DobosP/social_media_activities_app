package catalog

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestCasePortClaimsFileDecisionPartnerAndPrivateEvidence(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	owner := user(t, s, "claim-source-owner", "adult")
	staff := user(t, s, "claim-source-staff", "adult")
	staff.IsStaff = true
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_staff=true WHERE id=$1`, staff.ID); err != nil {
		t.Fatal(err)
	}
	place := placeFixture(t, s, "Sala Polivalentă", "osm", 23.6, 46.76)
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET website='' WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	input := ClaimInput{OrgName: "SC Sala Polivalentă SRL", OfficialWebsite: "https://sala.example/", ContactEmail: "private@example.invalid", CUI: "RO123456", Evidence: "PRIVATE-CLAIM-EVIDENCE"}
	id, err := s.FileClaim(ctx, owner, place, input)
	if err != nil || id < 1 {
		t.Fatal("file claim", id, err)
	}
	var status, kind, cui, evidence string
	var claimant, target int64
	if err := s.DB.QueryRow(ctx, `SELECT status,kind,cui,evidence,claimant_id,place_id FROM places_placeclaim WHERE id=$1`, id).Scan(&status, &kind, &cui, &evidence, &claimant, &target); err != nil || status != "pending" || kind != "business" || cui != "RO123456" || evidence != input.Evidence || claimant != owner.ID || target != place {
		t.Fatal("filed fields", status, kind, cui, err)
	}
	var count int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE event='places.claim_filed' AND actor_id=$1`, owner.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("file audit", count, err)
	}
	if _, err := s.FileClaim(ctx, owner, place, input); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("duplicate pending expected refusal", err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM places_placeclaim WHERE place_id=$1 AND claimant_id=$2`, place, owner.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate stored", count, err)
	}
	teen := user(t, s, "claim-source-teen", "teen")
	if _, err := s.FileClaim(ctx, teen, place, input); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("teen claimed", err)
	}
	if err := s.DecideClaim(ctx, owner, id, true, ""); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("nonstaff approved", err)
	}
	if err := s.DecideClaim(ctx, staff, id, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideClaim(ctx, staff, id, true, ""); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("repeat approved", err)
	}
	var partner int64
	var decider int64
	var decided bool
	if err := s.DB.QueryRow(ctx, `SELECT status,partner_id,decided_by_id,decided_at IS NOT NULL FROM places_placeclaim WHERE id=$1`, id).Scan(&status, &partner, &decider, &decided); err != nil || status != "approved" || partner < 1 || decider != staff.ID || !decided {
		t.Fatal("decision fields", status, partner, err)
	}
	var verified, active bool
	var partnerPlace int64
	var website string
	if err := s.DB.QueryRow(ctx, `SELECT kind,is_verified,is_active,place_id,website FROM places_partner WHERE id=$1`, partner).Scan(&kind, &verified, &active, &partnerPlace, &website); err != nil || kind != "business" || !verified || !active || partnerPlace != place || website != input.OfficialWebsite {
		t.Fatal("approved partner", err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT website FROM places_place WHERE id=$1`, place).Scan(&website); err != nil || website != input.OfficialWebsite {
		t.Fatal("website not backfilled", website, err)
	}
	var title, body, url string
	if err := s.DB.QueryRow(ctx, `SELECT title,body,url FROM notifications_notification WHERE recipient_id=$1 AND kind='system' ORDER BY id DESC LIMIT 1`, owner.ID).Scan(&title, &body, &url); err != nil || !strings.Contains(title, "approved") || !strings.Contains(body, input.OrgName) || !strings.Contains(body, "Sala Polivalentă") || url != "/places/"+strconv.FormatInt(place, 10)+"/" {
		t.Fatal("approval notice", title, body, url, err)
	}
	rows, err := s.Partners(ctx, &place, true)
	if err != nil || len(rows) != 1 || rows[0]["id"] != partner || len(rows[0]) != 6 {
		t.Fatal("public partner projection", rows, err)
	}
	for _, key := range []string{"contact_email", "cui", "evidence", "claimant_id", "decided_by_id"} {
		if _, ok := rows[0][key]; ok {
			t.Fatal("claim evidence public", key)
		}
	}
	second, err := s.FileClaim(ctx, owner, place, input)
	if err != nil {
		t.Fatal("new pending after decision", err)
	}
	if err := s.DecideClaim(ctx, staff, second, false, "  Domain email didn't match  "); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT status FROM places_placeclaim WHERE id=$1`, second).Scan(&status); err != nil || status != "rejected" {
		t.Fatal("rejection state", status, err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT title,body FROM notifications_notification WHERE recipient_id=$1 ORDER BY id DESC LIMIT 1`, owner.ID).Scan(&title, &body); err != nil || !strings.Contains(title, "not approved") || body != "It couldn't be verified: Domain email didn't match" {
		t.Fatal("rejection notice", title, body, err)
	}
}
