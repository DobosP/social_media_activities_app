package accounts

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

var ErrIdentityBound = errors.New("This identity is already linked to another account.")
var ErrIdentityBanned = errors.New("This identity is permanently banned and may not register.")

type ageClaims struct {
	Issuer       string          `json:"iss"`
	Subject      string          `json:"sub"`
	Audience     json.RawMessage `json:"aud"`
	Nonce        string          `json:"nonce"`
	Expires      float64         `json:"exp"`
	NotBefore    float64         `json:"nbf"`
	Issued       float64         `json:"iat"`
	Over16       *bool           `json:"age_over_16"`
	Over18       *bool           `json:"age_over_18"`
	Confirmation struct {
		JWK struct{ KTY, CRV, X, Y string } `json:"jwk"`
	} `json:"cnf"`
}

func parseJWS(raw string) ([]byte, []byte, []byte, error) {
	if len(raw) > 32<<10 {
		return nil, nil, nil, platform.ErrInvalid
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, nil, nil, platform.ErrInvalid
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, nil, nil, platform.ErrInvalid
	}
	var h struct {
		Alg  string
		Crit []string
	}
	if json.Unmarshal(header, &h) != nil || h.Alg != "ES256" || len(h.Crit) > 0 {
		return nil, nil, nil, platform.ErrInvalid
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, nil, nil, platform.ErrInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != 64 {
		return nil, nil, nil, platform.ErrInvalid
	}
	return []byte(parts[0] + "." + parts[1]), body, signature, nil
}
func verifyJWS(raw string, key *ecdsa.PublicKey) ([]byte, error) {
	message, body, signature, err := parseJWS(raw)
	if err != nil {
		return nil, err
	}
	if key == nil || key.Curve != elliptic.P256() || !key.Curve.IsOnCurve(key.X, key.Y) {
		return nil, platform.ErrInvalid
	}
	hash := sha256.Sum256(message)
	if !ecdsa.Verify(key, hash[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		return nil, platform.ErrInvalid
	}
	return body, nil
}
func trustedKey(raw string) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, platform.ErrInvalid
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, platform.ErrInvalid
	}
	public, ok := key.(*ecdsa.PublicKey)
	if !ok || public.Curve != elliptic.P256() {
		return nil, platform.ErrInvalid
	}
	return public, nil
}
func audienceMatches(raw json.RawMessage, wanted string) bool {
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return single == wanted
	}
	var list []string
	if json.Unmarshal(raw, &list) != nil {
		return false
	}
	for _, value := range list {
		if value == wanted {
			return true
		}
	}
	return false
}

func (s *Service) verifyAge(token, holderProof, nonce string) (ageClaims, string, error) {
	var claims ageClaims
	_, raw, _, err := parseJWS(token)
	if err != nil || json.Unmarshal(raw, &claims) != nil {
		return claims, "", platform.ErrInvalid
	}
	issuer := s.Config.TrustedIssuers[claims.Issuer]
	if issuer == "" {
		return claims, "", platform.ErrInvalid
	}
	key, err := trustedKey(issuer)
	if err != nil {
		return claims, "", err
	}
	raw, err = verifyJWS(token, key)
	if err != nil || json.Unmarshal(raw, &claims) != nil {
		return claims, "", platform.ErrInvalid
	}
	now := float64(s.Config.Now().Unix())
	if claims.Expires == 0 || claims.Expires+30 <= now || claims.NotBefore > now+30 || claims.Issued > now+30 || !audienceMatches(claims.Audience, s.Config.EUDIClientID) || nonce == "" || len(claims.Nonce) != len(nonce) || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return claims, "", platform.ErrInvalid
	}
	var all map[string]json.RawMessage
	if json.Unmarshal(raw, &all) != nil {
		return claims, "", platform.ErrInvalid
	}
	for _, key := range []string{"given_name", "family_name", "name", "birth_date", "birthdate", "date_of_birth", "age_in_years", "document_number", "personal_administrative_number", "portrait", "resident_address", "address", "nationality"} {
		if _, present := all[key]; present {
			return claims, "", platform.ErrInvalid
		}
	}
	if claims.Over16 == nil || claims.Over18 == nil || (*claims.Over18 && !*claims.Over16) {
		return claims, "", platform.ErrInvalid
	}
	proofStatus := "unverified"
	if holderProof != "" {
		jwk := claims.Confirmation.JWK
		if jwk.KTY != "EC" || jwk.CRV != "P-256" {
			return claims, "", platform.ErrInvalid
		}
		x, err := base64.RawURLEncoding.DecodeString(jwk.X)
		if err != nil || len(x) != 32 {
			return claims, "", platform.ErrInvalid
		}
		y, err := base64.RawURLEncoding.DecodeString(jwk.Y)
		if err != nil || len(y) != 32 {
			return claims, "", platform.ErrInvalid
		}
		holder := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		body, err := verifyJWS(holderProof, holder)
		if err != nil {
			return claims, "", err
		}
		var proof ageClaims
		if json.Unmarshal(body, &proof) != nil || !audienceMatches(proof.Audience, s.Config.EUDIClientID) || proof.Nonce != nonce || proof.Expires == 0 || proof.Expires+30 <= now || proof.NotBefore > now+30 || proof.Issued > now+30 {
			return claims, "", platform.ErrInvalid
		}
		proofStatus = "verified"
	}
	return claims, proofStatus, nil
}

func (s *Service) AgeStart(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	if s.Config.EUDIClientID == "" || len(s.Config.TrustedIssuers) == 0 {
		platform.Error(w, 503, "Age verification is not configured.")
		return
	}
	state, nonce := randomState(), randomState()
	allowed, err := s.allowAction(r.Context(), a.ID, "age_start", 30, time.Hour)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if !allowed {
		platform.Error(w, 429, "Try again later.")
		return
	}
	err = platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(683475951215)`); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM accounts_go_age_state WHERE expires_at<=now()`); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM accounts_go_age_state`).Scan(&count); err != nil {
			return err
		}
		if count >= 4096 {
			return platform.ErrForbidden
		}
		_, err := tx.Exec(r.Context(), `INSERT INTO accounts_go_age_state(state_hash,user_id,nonce,expires_at) VALUES($1,$2,$3,$4)`, hashState(state), a.ID, nonce, s.Config.Now().Add(10*time.Minute))
		return err
	})
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"nonce": nonce, "audience": s.Config.EUDIClientID, "state": state, "presentation_definition": map[string]any{"id": "age-verification", "input_descriptors": []any{map[string]any{"id": "age-attestation", "format": map[string]any{"jwt_vc": map[string]any{"alg": []string{"ES256"}}}, "constraints": map[string]any{"fields": []any{map[string]any{"path": []string{"$.age_over_16"}}, map[string]any{"path": []string{"$.age_over_18"}}}}}}}})
}

func (s *Service) AgeVerify(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	var body struct {
		State       string `json:"state"`
		Token       string `json:"vp_token"`
		HolderProof string `json:"holder_binding_proof"`
	}
	if platform.Decode(w, r, &body) != nil {
		platform.Fail(w, platform.ErrInvalid)
		return
	}
	var nonce string
	err := s.DB.QueryRow(r.Context(), `SELECT nonce FROM accounts_go_age_state WHERE state_hash=$1 AND user_id=$2 AND expires_at>$3`, hashState(body.State), a.ID, s.Config.Now()).Scan(&nonce)
	if err != nil {
		platform.Error(w, 400, "Invalid or expired verification state; restart verification.")
		return
	}
	claims, holderProof, err := s.verifyAge(body.Token, body.HolderProof, nonce)
	if err != nil {
		platform.Error(w, 400, "Invalid wallet age presentation.")
		return
	}
	// Consume after signature verification and before account mutation: even a
	// rejected duplicate/banned identity cannot replay a completed ceremony.
	var consumed string
	err = s.DB.QueryRow(r.Context(), `DELETE FROM accounts_go_age_state WHERE state_hash=$1 AND user_id=$2 AND expires_at>$3 RETURNING nonce`, hashState(body.State), a.ID, s.Config.Now()).Scan(&consumed)
	if err != nil {
		platform.Error(w, 400, "This verification has already been used.")
		return
	}
	err = platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO accounts_consumedagenonce(nonce,created_at) VALUES($1,now())`, nonce); err != nil {
			return err
		}
		if err := s.bindIdentity(r.Context(), tx, a, claims.Subject, holderProof == "verified"); err != nil {
			return err
		}
		band, cohort := "under_16", "child"
		if *claims.Over18 {
			band, cohort = "adult", "adult"
		} else if *claims.Over16 {
			band, cohort = "16_17", "teen"
		}
		// Adulthood ends guardian authority in the same transaction (ADR-0045).
		if cohort == "adult" {
			if err := revokeAdultWard(r.Context(), tx, a); err != nil {
				return err
			}
		}
		if a.Cohort != cohort && a.Cohort != "unassigned" {
			if err := evictParticipation(r.Context(), tx, a, "cohort_changed"); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(r.Context(), `UPDATE accounts_user SET age_band=$2,cohort=$3,is_identity_verified=true,identity_verified_at=now() WHERE id=$1`, a.ID, band, cohort); err != nil {
			return err
		}
		evidence, _ := json.Marshal(map[string]any{"age_over_16": *claims.Over16, "age_over_18": *claims.Over18, "format": "jwt_vc", "holder_proof": holderProof})
		_, err := tx.Exec(r.Context(), `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'eudi','openid4vp',$2,now(),$3,$4,'')`, a.ID, band, time.Unix(int64(claims.Expires), 0), evidence)
		if err != nil {
			return err
		}
		return platform.RecordAudit(r.Context(), tx, a, "age.verified", "accounts.user:"+strconv.FormatInt(a.ID, 10), nil)
	})
	if errors.Is(err, ErrIdentityBound) {
		platform.Error(w, 409, err.Error())
		return
	}
	if errors.Is(err, ErrIdentityBanned) {
		platform.Error(w, 403, err.Error())
		return
	}
	if err != nil {
		platform.Fail(w, err)
		return
	}
	a, err = s.actor(r.Context(), s.DB, a.ID)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	payload, err := s.Self(r.Context(), s.DB, a)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, payload)
}

func (s *Service) bindIdentity(ctx context.Context, tx pgx.Tx, a platform.Actor, subject string, proof bool) error {
	if !s.Config.IdentityUniquenessEnforced || !proof || subject == "" {
		return nil
	}
	if len(s.Secret) < 32 {
		return platform.ErrForbidden
	}
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = mac.Write([]byte(subject))
	holder := hex.EncodeToString(mac.Sum(nil))
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, holder); err != nil {
		return err
	}
	var banned bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_bannedidentity WHERE holder_hash=$1)`, holder).Scan(&banned); err != nil {
		return err
	}
	if banned {
		return ErrIdentityBanned
	}
	var id int64
	var user *int64
	var released *time.Time
	err := tx.QueryRow(ctx, `SELECT id,user_id,released_at FROM accounts_identitybinding WHERE holder_hash=$1 FOR UPDATE`, holder).Scan(&id, &user, &released)
	if err == nil {
		if user != nil && *user == a.ID {
			return nil
		}
		if user != nil && released == nil {
			return ErrIdentityBound
		}
		if _, err = tx.Exec(ctx, `UPDATE accounts_identitybinding SET user_id=$2,released_at=NULL WHERE id=$1`, id, a.ID); err != nil {
			return err
		}
		return platform.RecordAudit(ctx, tx, a, "identity.bound", "accounts.identitybinding:"+strconv.FormatInt(id, 10), map[string]bool{"recovery": true})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var has bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_identitybinding WHERE user_id=$1)`, a.ID).Scan(&has); err != nil {
		return err
	}
	if has {
		// Re-verification must prove the same bound wallet. A different wallet
		// cannot change this account's age cohort while retaining the old binding.
		return ErrIdentityBound
	}
	if err = tx.QueryRow(ctx, `INSERT INTO accounts_identitybinding(holder_hash,user_id,created_at,released_at) VALUES($1,$2,now(),NULL) RETURNING id`, holder, a.ID).Scan(&id); err != nil {
		return err
	}
	return platform.RecordAudit(ctx, tx, a, "identity.bound", "accounts.identitybinding:"+strconv.FormatInt(id, 10), map[string]bool{"recovery": false})
}
