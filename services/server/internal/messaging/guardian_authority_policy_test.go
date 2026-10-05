package messaging

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// Owner decision (ADR-0045): an actual signed adult re-verification ends every
// guardianship over the new adult, with their consents and the guardian's
// observer seat, in the AgeVerify transaction. Unrelated wards stay linked.
func TestGuardianAuthorityPolicyAdultReverificationRevokesGuardianship(t *testing.T) {
	s, acc := privacy6Fixture(t)
	ctx := context.Background()
	a := testdb.Actor(t, s.DB, "policy-adulthood-ward", "child")
	b := testdb.Actor(t, s.DB, "policy-adulthood-peer", "child")
	g := testdb.Actor(t, s.DB, "policy-adulthood-guardian", "adult")
	co := testdb.Actor(t, s.DB, "policy-adulthood-coguardian", "adult")
	other := testdb.Actor(t, s.DB, "policy-adulthood-other-ward", "child")
	for _, who := range []platform.Actor{a, b, g} {
		if _, err := s.RegisterKey(ctx, who, jwk(who.Username), "", nil); err != nil {
			t.Fatal(err)
		}
	}
	id := casePort3ActiveDirect(t, s, a, b)
	casePort3GuardianLink(t, s, g, a)
	casePort3GuardianLink(t, s, co, a)
	casePort3GuardianLink(t, s, g, other)
	if err := s.AddGuardian(ctx, g, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ParticipantKeys(ctx, g, id); err != nil {
		t.Fatal("baseline guardian observer reading failed", err)
	}
	guardianCall := func(method, path, body string) *httptest.ResponseRecorder {
		r := platform.WithActor(httptest.NewRequest(method, path, strings.NewReader(body)), g)
		mux := http.NewServeMux()
		acc.Register(mux)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	wardPath := "/api/accounts/wards/" + a.PublicID + "/"
	if out := guardianCall("GET", wardPath+"export/", ""); out.Code != 200 {
		t.Fatal("baseline guardian ward export failed", out.Code)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	acc.Config.EUDIClientID = "synthetic-local-policy-adulthood"
	acc.Config.TrustedIssuers = map[string]string{"synthetic-local-age-issuer": string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))}
	start := httptest.NewRecorder()
	acc.AgeStart(start, platform.WithActor(httptest.NewRequest("POST", "/api/accounts/age/start/", nil), a))
	if start.Code != 200 {
		t.Fatal("synthetic local age start", start.Code)
	}
	state := object(t, start)
	now := time.Now()
	token := privacy6SignedJWT(t, key, map[string]any{"iss": "synthetic-local-age-issuer", "aud": acc.Config.EUDIClientID, "nonce": state["nonce"], "exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "age_over_16": true, "age_over_18": true})
	raw, _ := json.Marshal(map[string]any{"state": state["state"], "vp_token": token})
	verify := httptest.NewRecorder()
	acc.AgeVerify(verify, platform.WithActor(httptest.NewRequest("POST", "/api/accounts/age/verify/", strings.NewReader(string(raw))), a))
	if verify.Code != 200 {
		t.Fatal("signed synthetic adult age verification", verify.Code)
	}
	var cohort string
	var links, activeConsents, revokedConsents, otherLinks int
	if err := s.DB.QueryRow(ctx, `SELECT u.cohort,(SELECT count(*) FROM accounts_guardianrelationship WHERE ward_id=u.id AND status='active'),(SELECT count(*) FROM accounts_parentalconsent WHERE minor_id=u.id AND status='active'),(SELECT count(*) FROM accounts_parentalconsent WHERE minor_id=u.id AND status='revoked' AND revoked_at IS NOT NULL),(SELECT count(*) FROM accounts_guardianrelationship WHERE ward_id=$2 AND guardian_id=$3 AND status='active') FROM accounts_user u WHERE u.id=$1`, a.ID, other.ID, g.ID).Scan(&cohort, &links, &activeConsents, &revokedConsents, &otherLinks); err != nil || cohort != "adult" || links != 0 || activeConsents != 0 || revokedConsents != 1 || otherLinks != 1 {
		t.Fatal("adult re-verification left guardian authority or touched an unrelated ward", err, cohort, links, activeConsents, revokedConsents, otherLinks)
	}
	for _, guardian := range []platform.Actor{g, co} {
		var audits int
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE event='guardian.revoked' AND actor_id=$1 AND target_ref=$2 AND data->>'reason'='ward_adult'`, a.ID, fmt.Sprintf("accounts.user:%d", guardian.ID)).Scan(&audits); err != nil || audits != 1 {
			t.Fatal("adulthood revocation was not audited per guardian", err, audits)
		}
	}
	var seat string
	if err := s.DB.QueryRow(ctx, `SELECT state FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2`, id, g.ID).Scan(&seat); err != nil || seat != "removed" {
		t.Fatal("former guardian kept an observer seat", err, seat)
	}
	if _, err := s.ParticipantKeys(ctx, g, id); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("former guardian still reads the key roster", err)
	}
	for _, scenario := range []struct{ method, path, body string }{
		{"PATCH", wardPath, `{"display_name":"Renamed by former guardian"}`},
		{"GET", wardPath + "export/", ""},
		{"DELETE", wardPath, ""},
	} {
		if out := guardianCall(scenario.method, scenario.path, scenario.body); out.Code != 403 {
			t.Fatal("former guardian kept authority over the adult", scenario.method, scenario.path, out.Code)
		}
	}
	var display string
	if err := s.DB.QueryRow(ctx, `SELECT display_name FROM accounts_user WHERE id=$1`, a.ID).Scan(&display); err != nil || display != a.DisplayName {
		t.Fatal("refused former guardian changed or erased the adult", err)
	}
	listing := guardianCall("GET", "/api/accounts/wards/", "")
	var wards []map[string]any
	if listing.Code != 200 || json.Unmarshal(listing.Body.Bytes(), &wards) != nil || len(wards) != 1 || wards[0]["username"] != other.Username {
		t.Fatal("former guardian ward list still names the adult", listing.Code)
	}
}
