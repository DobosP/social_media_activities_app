package accounts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestRetirementEUDIActualAgeBandAndConsentMatrix(t *testing.T) {
	s := accountFixture(t)
	issuer, public := testESKey(t)
	s.Config.TrustedIssuers = map[string]string{"retirement-issuer": public}
	s.Config.IdentityUniquenessEnforced = false
	s.Config.AllowMinorOnboarding = true // isolated synthetic account fixture only
	for _, scenario := range []struct {
		name, band, cohort          string
		over16, over18, participate bool
	}{{"adult", "adult", "adult", true, true, true}, {"teen", "16_17", "teen", true, false, true}, {"child", "under_16", "child", false, false, false}} {
		t.Run(scenario.name, func(t *testing.T) {
			user := accountUser(t, s, "retirement-eudi-"+scenario.name, "unknown", "unassigned")
			start := accountRequest(s, user, "POST", "/api/accounts/verify-age/start/", `{}`)
			if start.Code != 200 {
				t.Fatal("age ceremony did not start")
			}
			flow := accountJSON(t, start)
			credential := testESJWT(t, issuer, map[string]any{"iss": "retirement-issuer", "aud": s.Config.EUDIClientID, "nonce": flow["nonce"], "sub": "synthetic-" + scenario.name, "exp": time.Now().Add(time.Hour).Unix(), "age_over_16": scenario.over16, "age_over_18": scenario.over18})
			body, _ := json.Marshal(map[string]any{"state": flow["state"], "vp_token": credential})
			finish := accountRequest(s, user, "POST", "/api/accounts/verify-age/", string(body))
			if finish.Code != 200 {
				t.Fatal("signed synthetic age credential rejected", finish.Code)
			}
			self := accountJSON(t, finish)
			if self["age_band"] != scenario.band || self["cohort"] != scenario.cohort || self["is_identity_verified"] != true || self["can_participate"] != scenario.participate {
				t.Fatal("signed age claims mapped to wrong band/cohort/consent authority")
			}
			var consents int
			if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM accounts_parentalconsent WHERE minor_id=$1`, user.ID).Scan(&consents); err != nil || consents != 0 {
				t.Fatal("age assurance fabricated parental consent")
			}
		})
	}
}

func TestRetirementEUDIHolderProofAdversarialMatrix(t *testing.T) {
	issuer, public := testESKey(t)
	holder, _ := testESKey(t)
	wrong, _ := testESKey(t)
	s := New(nil, nil, "", Config{EUDIClientID: "retirement-client", TrustedIssuers: map[string]string{"retirement-issuer": public}})
	claims := func() map[string]any {
		return map[string]any{"iss": "retirement-issuer", "aud": "retirement-client", "nonce": "retirement-nonce", "sub": "synthetic-holder", "exp": time.Now().Add(time.Hour).Unix(), "age_over_16": true, "age_over_18": true, "cnf": map[string]any{"jwk": map[string]string{"kty": "EC", "crv": "P-256", "x": base64.RawURLEncoding.EncodeToString(holder.X.FillBytes(make([]byte, 32))), "y": base64.RawURLEncoding.EncodeToString(holder.Y.FillBytes(make([]byte, 32)))}}}
	}
	for _, scenario := range []struct {
		name                                 string
		wrongKey, wrongNonce, noConfirmation bool
	}{{"wrong-holder-key", true, false, false}, {"replayed-holder-nonce", false, true, false}, {"credential-without-confirmation", false, false, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			proofKey := holder
			if scenario.wrongKey {
				proofKey = wrong
			}
			nonce := "retirement-nonce"
			if scenario.wrongNonce {
				nonce = "previous-nonce"
			}
			credentialClaims := claims()
			if scenario.noConfirmation {
				delete(credentialClaims, "cnf")
			}
			proof := testESJWT(t, proofKey, map[string]any{"aud": "retirement-client", "nonce": nonce, "exp": time.Now().Add(time.Minute).Unix()})
			if _, _, err := s.verifyAge(testESJWT(t, issuer, credentialClaims), proof, "retirement-nonce"); err == nil {
				t.Fatal("invalid holder binding was accepted")
			}
		})
	}
	verified, proofStatus, err := s.verifyAge(testESJWT(t, issuer, claims()), "", "retirement-nonce")
	if err != nil || proofStatus != "unverified" || verified.Subject != "synthetic-holder" {
		t.Fatal("missing holder proof silently claimed possession")
	}
	token := testESJWT(t, issuer, claims())
	parts := []byte(token)
	parts[len(parts)-4] = 'A'
	if string(parts) == token {
		parts[len(parts)-4] = 'B'
	}
	if _, _, err := s.verifyAge(string(parts), "", "retirement-nonce"); err == nil {
		t.Fatal("tampered signed credential was accepted")
	}
}
