package accounts

import (
	"context"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestRootAuditCorrectionIssuedTokenExportContainsOnlyIssuanceMetadata(t *testing.T) {
	s := accountFixture(t)
	a := accountUser(t, s, "root-audit-token-owner", "adult", "adult")
	const password = "Synthetic-root-audit-token-password-9236"
	hash, err := authcore.HashPassword(context.Background(), password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(context.Background(), `UPDATE accounts_user SET password=$2 WHERE id=$1`, a.ID, hash); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"username": a.Username, "password": password})
	issued := accountRequest(s, platform.Actor{}, "POST", "/api/auth/token/", string(body))
	if issued.Code != 200 {
		t.Fatal("synthetic device credential issue failed", issued.Code)
	}
	var tokenBody map[string]any
	if err := json.Unmarshal(issued.Body.Bytes(), &tokenBody); err != nil {
		t.Fatal(err)
	}
	token := tokenBody["token"].(string)
	out := accountRequest(s, a, "GET", "/api/accounts/me/export/", "")
	if out.Code != 200 {
		t.Fatal("actual own export failed", out.Code)
	}
	var export map[string]any
	if err := json.Unmarshal(out.Body.Bytes(), &export); err != nil {
		t.Fatal(err)
	}
	access := export["api_access"].(map[string]any)
	if len(access) != 2 || access["api_token_issued"] != true || access["issued_at"] == nil {
		t.Fatal("issued token metadata omitted or widened")
	}
	if strings.Contains(out.Body.String(), token) {
		t.Fatal("issued device credential appeared in whole export")
	}
}

func rootAuditAgePresentation(t *testing.T, s *Service, issuer, holder *ecdsa.PrivateKey, a platform.Actor, subject string) int {
	t.Helper()
	started := accountRequest(s, a, "POST", "/api/accounts/verify-age/start/", `{}`)
	if started.Code != 200 {
		t.Fatal("synthetic age ceremony start failed", started.Code)
	}
	var flow map[string]any
	if err := json.Unmarshal(started.Body.Bytes(), &flow); err != nil {
		t.Fatal(err)
	}
	credential := testESJWT(t, issuer, map[string]any{"iss": "root-audit-issuer", "aud": s.Config.EUDIClientID, "nonce": flow["nonce"], "sub": subject, "exp": time.Now().Add(time.Hour).Unix(), "age_over_16": true, "age_over_18": true, "cnf": map[string]any{"jwk": map[string]string{"kty": "EC", "crv": "P-256", "x": base64.RawURLEncoding.EncodeToString(holder.X.FillBytes(make([]byte, 32))), "y": base64.RawURLEncoding.EncodeToString(holder.Y.FillBytes(make([]byte, 32)))}}})
	proof := testESJWT(t, holder, map[string]any{"aud": s.Config.EUDIClientID, "nonce": flow["nonce"], "exp": time.Now().Add(time.Minute).Unix()})
	body, _ := json.Marshal(map[string]any{"state": flow["state"], "vp_token": credential, "holder_binding_proof": proof})
	return accountRequest(s, a, "POST", "/api/accounts/verify-age/", string(body)).Code
}

func TestRootAuditCorrectionHolderSubjectNeverPersistsBeyondKeyedBinding(t *testing.T) {
	s := accountFixture(t)
	issuer, public := testESKey(t)
	holder, _ := testESKey(t)
	s.Config.TrustedIssuers = map[string]string{"root-audit-issuer": public}
	s.Config.IdentityUniquenessEnforced = true
	a := accountUser(t, s, "root-audit-holder-owner", "unknown", "unassigned")
	const subject = "synthetic-root-audit-private-holder-subject"
	if status := rootAuditAgePresentation(t, s, issuer, holder, a, subject); status != 200 {
		t.Fatal("signed holder-bound API proof rejected", status)
	}
	var raw json.RawMessage
	if err := s.DB.QueryRow(context.Background(), `SELECT raw FROM accounts_ageassurance WHERE user_id=$1`, a.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for key := range meta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"age_over_16", "age_over_18", "format", "holder_proof"}) || meta["age_over_16"] != true || meta["age_over_18"] != true || meta["holder_proof"] != "verified" || strings.Contains(string(raw), subject) {
		t.Fatal("assurance META escaped exact source minimization contract")
	}
	var binding string
	if err := s.DB.QueryRow(context.Background(), `SELECT holder_hash FROM accounts_identitybinding WHERE user_id=$1`, a.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = mac.Write([]byte(subject))
	if binding != hex.EncodeToString(mac.Sum(nil)) || strings.Contains(binding, subject) {
		t.Fatal("holder binding was not opaque keyed hash")
	}
}

func TestRootAuditCorrectionBannedWalletAPILeavesNewcomerAndBindingUnchanged(t *testing.T) {
	s := accountFixture(t)
	issuer, public := testESKey(t)
	holder, _ := testESKey(t)
	s.Config.TrustedIssuers = map[string]string{"root-audit-issuer": public}
	s.Config.IdentityUniquenessEnforced = true
	owner := accountUser(t, s, "root-audit-banned-owner", "unknown", "unassigned")
	newcomer := accountUser(t, s, "root-audit-banned-newcomer", "unknown", "unassigned")
	const subject = "synthetic-root-audit-banned-wallet"
	if status := rootAuditAgePresentation(t, s, issuer, holder, owner, subject); status != 200 {
		t.Fatal("fixture wallet owner proof failed", status)
	}
	var binding string
	if err := s.DB.QueryRow(context.Background(), `SELECT holder_hash FROM accounts_identitybinding WHERE user_id=$1`, owner.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO accounts_bannedidentity(holder_hash,created_at) VALUES($1,now())`, binding); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM accounts_identitybinding`).Scan(&before); err != nil || before != 1 {
		t.Fatal("fixture binding count wrong", err)
	}
	if status := rootAuditAgePresentation(t, s, issuer, holder, newcomer, subject); status != 403 {
		t.Fatal("banned wallet API status changed", status)
	}
	var verified bool
	var band, cohort string
	if err := s.DB.QueryRow(context.Background(), `SELECT is_identity_verified,age_band,cohort FROM accounts_user WHERE id=$1`, newcomer.ID).Scan(&verified, &band, &cohort); err != nil || verified || band != "unknown" || cohort != "unassigned" {
		t.Fatal("banned newcomer received identity authority", err)
	}
	var after, assurances int
	if err := s.DB.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM accounts_identitybinding),(SELECT count(*) FROM accounts_ageassurance WHERE user_id=$1)`, newcomer.ID).Scan(&after, &assurances); err != nil || after != before || assurances != 0 {
		t.Fatal("banned API rejection applied assurance or new binding", err)
	}
}
