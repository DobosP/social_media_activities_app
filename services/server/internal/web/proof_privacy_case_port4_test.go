package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestPrivacyCasePort4AgeProvenanceMinimizationAndExpiryStatus(t *testing.T) {
	s, a := accountWebFixture(t)
	ctx := context.Background()
	unverified := testdb.Actor(t, s.DB, "privacy-case4-unverified-proof", "unassigned")
	unverified.IdentityVerified = false
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_identity_verified=false,age_band='unknown' WHERE id=$1`, unverified.ID); err != nil {
		t.Fatal(err)
	}
	if proof, err := s.accountProvenance(ctx, unverified); err != nil || proof != nil {
		t.Fatal("unverified user acquired proof provenance", err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'Synthetic issuer','openid4vp','adult',now(),NULL,'{"private_marker":"synthetic-attestation-internal-marker"}','')`, a.ID); err != nil {
		t.Fatal(err)
	}
	proof, err := s.accountProvenance(ctx, a)
	if err != nil || proof["has_row"] != true || proof["band_display"] == "" {
		t.Fatal("proof provenance label/row missing", err)
	}
	for _, field := range []string{"raw", "age_band", "dob", "date_of_birth"} {
		if _, present := proof[field]; present {
			t.Fatal("provenance exposed source-forbidden PII field", field)
		}
	}
	raw, _ := json.Marshal(proof)
	if strings.Contains(string(raw), "synthetic-attestation-internal-marker") {
		t.Fatal("raw attestation escaped provenance projection")
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_ageassurance SET expires_at=now()-interval '1 day' WHERE user_id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	proof, err = s.accountProvenance(ctx, a)
	if err != nil || proof["status"] != "expired" || proof["is_current"] != false || proof["expires_soon"] != false {
		t.Fatal("expired proof provenance status drifted", err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_ageassurance SET expires_at=now()+interval '3 days' WHERE user_id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	proof, err = s.accountProvenance(ctx, a)
	if err != nil || proof["status"] != "expiring" || proof["expires_soon"] != true || proof["days_left"].(int) < 0 || proof["days_left"].(int) > 14 {
		t.Fatal("expiring proof status/window drifted", err)
	}
}

func TestPrivacyCasePort4RetentionUsesLiveDurationsMinorFloorsAndOperativeProof(t *testing.T) {
	s, a := accountWebFixture(t)
	ctx := context.Background()
	child := testdb.Actor(t, s.DB, "privacy-case4-retention-child", "child")
	s.Accounts.Config.GuardianInviteTTL = 7 * 24 * time.Hour
	s.Accounts.Config.APITokenTTL = 90 * 24 * time.Hour
	s.Config.AccountRetention = AccountRetentionConfig{MessagingDays: 0, AdultPhotoMinimumSeconds: 3600, MinorPhotoMinimumSeconds: 86400}
	byCategory := func(rows []map[string]string) map[string]string {
		out := map[string]string{}
		for _, row := range rows {
			if len(row) != 2 {
				t.Fatal("retention row was not durations-only")
			}
			out[row["category"]] = row["ttl_description"]
		}
		return out
	}
	rows := s.accountRetention(a, nil)
	if len(rows) == 0 {
		t.Fatal("retention disclosure empty")
	}
	adult := byCategory(rows)
	if !strings.Contains(adult["Guardian invitations"], "7 days") || !strings.Contains(adult["Device app access"], "90 days") {
		t.Fatal("live invite/token durations not disclosed")
	}
	if !strings.Contains(adult["Private (encrypted) messages"], "no automatic deletion") || strings.Contains(adult["Private (encrypted) messages"], "days after they're sent") {
		t.Fatal("disabled retention falsely promised deletion")
	}
	if !strings.Contains(adult["Age verification"], "no set expiry") {
		t.Fatal("unset proof expiry was invented")
	}
	if !strings.Contains(adult["Disappearing photos"], "1 hour") || !strings.Contains(byCategory(s.accountRetention(child, nil))["Disappearing photos"], "1 day") {
		t.Fatal("minor/adult disappearing-photo floors drifted")
	}
	s.Config.AccountRetention.MessagingDays = 30
	if !strings.Contains(byCategory(s.accountRetention(a, nil))["Private (encrypted) messages"], "30 days") {
		t.Fatal("positive retention duration omitted")
	}
	verified := time.Now().UTC().Truncate(time.Microsecond)
	expires := verified.Add(365 * 24 * time.Hour)
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'Synthetic issuer','fixture','adult',$2,NULL,'{}',''),($1,'Synthetic issuer','fixture','adult',$2,$3,'{}','')`, a.ID, verified, expires); err != nil {
		t.Fatal(err)
	}
	proof, err := s.accountProvenance(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	text := byCategory(s.accountRetention(a, proof))["Age verification"]
	if !strings.Contains(text, "expires on") || !strings.Contains(text, expires.Format("2006")) || strings.Contains(text, "no set expiry") {
		t.Fatal("verified_at tie selected old proof or invented expiry")
	}
}
