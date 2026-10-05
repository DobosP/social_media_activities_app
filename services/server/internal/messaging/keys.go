package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

// Backups are opaque client-encrypted objects. Public/backup validation forbids
// accidental clear private-key or passphrase fields; the server has no decryptor.
func opaqueBackup(value any) bool {
	if value == nil {
		return true
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return false
	}
	raw, e := json.Marshal(obj)
	if e != nil || len(raw) > 64<<10 {
		return false
	}
	var visit func(any, int) bool
	visit = func(v any, depth int) bool {
		if depth > 12 {
			return false
		}
		switch item := v.(type) {
		case map[string]any:
			for key, child := range item {
				switch strings.ToLower(key) {
				case "d", "p", "q", "dp", "dq", "qi", "oth", "k", "passphrase", "password", "private_jwk", "private_key":
					return false
				}
				if !visit(child, depth+1) {
					return false
				}
			}
		case []any:
			for _, child := range item {
				if !visit(child, depth+1) {
					return false
				}
			}
		}
		return true
	}
	return visit(obj, 0)
}
func (s *Service) RegisterKey(ctx context.Context, a platform.Actor, jwk map[string]any, algorithm string, backup any) (map[string]any, error) {
	if !publicJWK(jwk) || !opaqueBackup(backup) {
		return nil, platform.ErrInvalid
	}
	if algorithm == "" {
		algorithm = "ECDH-P256"
	}
	if len(algorithm) > 32 {
		return nil, platform.ErrInvalid
	}
	raw, e := json.Marshal(jwk)
	if e != nil || len(raw) > 8<<10 {
		return nil, platform.ErrInvalid
	}
	backupRaw, e := json.Marshal(backup)
	if e != nil {
		return nil, platform.ErrInvalid
	}
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		fresh, e := actor(ctx, tx, a.ID)
		if e != nil {
			return e
		}
		a = fresh
		if e = platform.Participate(ctx, tx, a); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `SELECT id FROM accounts_user WHERE id=$1 FOR UPDATE`, a.ID); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE messaging_publickey SET active=false WHERE user_id=$1 AND active`, a.ID); e != nil {
			return e
		}
		var keyID string
		if e = tx.QueryRow(ctx, `INSERT INTO messaging_publickey(user_id,key_id,algorithm,public_jwk,wrapped_private_jwk,active,created_at) VALUES($1,gen_random_uuid(),$2,$3,$4,true,now()) RETURNING key_id::text`, a.ID, algorithm, raw, backupRaw).Scan(&keyID); e != nil {
			return e
		}
		return platform.RecordAudit(ctx, tx, a, "messaging.key_registered", "", map[string]any{"key_id": keyID})
	})
	if err != nil {
		return nil, err
	}
	return s.OwnKey(ctx, a)
}
func (s *Service) OwnKey(ctx context.Context, a platform.Actor) (map[string]any, error) {
	var id, algorithm string
	var public, backup json.RawMessage
	var created time.Time
	e := s.DB.QueryRow(ctx, `SELECT key_id::text,algorithm,public_jwk,wrapped_private_jwk,created_at FROM messaging_publickey WHERE user_id=$1 AND active`, a.ID).Scan(&id, &algorithm, &public, &backup, &created)
	if e != nil {
		return nil, e
	}
	return map[string]any{"key_id": id, "algorithm": algorithm, "public_jwk": public, "wrapped_private_jwk": backup, "created_at": created}, nil
}
func keyStatus(ctx context.Context, q platform.Querier, viewer, subject int64, raw []byte) (map[string]any, error) {
	var obj any
	if json.Unmarshal(raw, &obj) != nil {
		return nil, platform.ErrInvalid
	}
	fingerprint, e := keyFingerprint(obj)
	if e != nil {
		return nil, e
	}
	var verified bool
	e = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM messaging_keyverification WHERE verifier_id=$1 AND subject_id=$2 AND fingerprint=$3)`, viewer, subject, fingerprint).Scan(&verified)
	if e != nil {
		return nil, e
	}
	return map[string]any{"fingerprint": fingerprint, "verified": verified}, nil
}
func (s *Service) ContactKey(ctx context.Context, a platform.Actor, username string) (map[string]any, error) {
	a, e := actor(ctx, s.DB, a.ID)
	if e != nil {
		return nil, e
	}
	b, e := target(ctx, s.DB, username)
	if e != nil {
		return nil, e
	}
	if e = pair(ctx, s.DB, a, b); e != nil {
		if errors.Is(e, platform.ErrForbidden) {
			return nil, platform.ErrNotFound
		}
		return nil, e
	}
	var id, algorithm string
	var public json.RawMessage
	var created time.Time
	if e = s.DB.QueryRow(ctx, `SELECT key_id::text,algorithm,public_jwk,created_at FROM messaging_publickey WHERE user_id=$1 AND active`, b.ID).Scan(&id, &algorithm, &public, &created); e != nil {
		return nil, e
	}
	ref, e := userRef(ctx, s.DB, b.ID)
	if e != nil {
		return nil, e
	}
	status, e := keyStatus(ctx, s.DB, a.ID, b.ID, public)
	if e != nil {
		return nil, e
	}
	status["key_id"] = id
	status["algorithm"] = algorithm
	status["public_jwk"] = public
	status["user"] = ref
	status["created_at"] = created
	return status, nil
}
func (s *Service) VerifyKey(ctx context.Context, a platform.Actor, username, fingerprint string) (map[string]any, error) {
	if len(fingerprint) != 32 {
		return nil, platform.ErrInvalid
	}
	var status map[string]any
	e := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		fresh, e := actor(ctx, tx, a.ID)
		if e != nil {
			return e
		}
		a = fresh
		b, e := target(ctx, tx, username)
		if e != nil {
			return e
		}
		if e = pair(ctx, tx, a, b); e != nil {
			return e
		}
		var raw json.RawMessage
		if e = tx.QueryRow(ctx, `SELECT public_jwk FROM messaging_publickey WHERE user_id=$1 AND active FOR SHARE`, b.ID).Scan(&raw); e != nil {
			return e
		}
		var obj any
		if json.Unmarshal(raw, &obj) != nil {
			return platform.ErrInvalid
		}
		current, e := keyFingerprint(obj)
		if e != nil || current != fingerprint {
			return platform.ErrInvalid
		}
		if _, e = tx.Exec(ctx, `INSERT INTO messaging_keyverification(verifier_id,subject_id,fingerprint,created_at,updated_at) VALUES($1,$2,$3,now(),now()) ON CONFLICT(verifier_id,subject_id) DO UPDATE SET fingerprint=excluded.fingerprint,updated_at=now()`, a.ID, b.ID, current); e != nil {
			return e
		}
		if e = platform.RecordAudit(ctx, tx, a, "messaging.key_verified", fmt.Sprintf("accounts.user:%d", b.ID), nil); e != nil {
			return e
		}
		status = map[string]any{"fingerprint": current, "verified": true}
		return nil
	})
	return status, e
}
func (s *Service) ParticipantKeys(ctx context.Context, a platform.Actor, id int64) ([]any, error) {
	// Read current viewer authority together with the participant/cohort row.
	// Combining these predicates preserves the ordinary CanView gates without
	// turning a bounded key roster into per-participant permission queries.
	var role, kind string
	var ok bool
	e := s.DB.QueryRow(ctx, `SELECT mine.role,c.kind,u.is_active AND mine.state='active' AND (
mine.role='guardian' OR (
u.is_identity_verified AND u.cohort<>'' AND u.cohort<>'unassigned' AND u.cohort=c.cohort
AND COALESCE((SELECT expires_at IS NULL OR expires_at>now() FROM accounts_ageassurance WHERE user_id=u.id ORDER BY verified_at DESC,id DESC LIMIT 1),true)
AND (u.age_band<>'under_16' OR EXISTS(SELECT 1 FROM accounts_parentalconsent WHERE minor_id=u.id AND status='active' AND (expires_at IS NULL OR expires_at>now())))
AND NOT EXISTS(SELECT 1 FROM messaging_participant p JOIN safety_block b ON (b.blocker_id=u.id AND b.blocked_id=p.user_id) OR (b.blocker_id=p.user_id AND b.blocked_id=u.id) WHERE p.conversation_id=c.id AND p.state='active' AND p.role<>'guardian')
)) FROM accounts_user u JOIN messaging_participant mine ON mine.user_id=u.id JOIN messaging_conversation c ON c.id=mine.conversation_id WHERE u.id=$1 AND c.id=$2`, a.ID, id).Scan(&role, &kind, &ok)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, platform.ErrForbidden
	}
	if e != nil {
		return nil, e
	}
	if ok && role == "guardian" {
		ok, e = s.CanView(ctx, s.DB, a, id)
		if e != nil {
			return nil, e
		}
	}
	if !ok {
		return nil, platform.ErrForbidden
	}
	rows, e := s.DB.Query(ctx, `SELECT u.public_id::text,u.username,u.display_name,p.role,k.public_jwk FROM messaging_participant p JOIN accounts_user u ON u.id=p.user_id JOIN messaging_publickey k ON k.user_id=u.id AND k.active WHERE p.conversation_id=$1 AND p.state='active' ORDER BY p.id LIMIT 257`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []any{}
	for rows.Next() {
		var publicID, username, display, role string
		var jwk json.RawMessage
		if e = rows.Scan(&publicID, &username, &display, &role, &jwk); e != nil {
			return nil, e
		}
		var obj any
		if json.Unmarshal(jwk, &obj) != nil {
			return nil, platform.ErrInvalid
		}
		fingerprint, e := keyFingerprint(obj)
		if e != nil {
			return nil, e
		}
		if display == "" {
			display = username
		}
		result = append(result, map[string]any{"public_id": publicID, "username": username, "display_name": display, "role": role, "public_jwk": jwk, "fingerprint": fingerprint})
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	cap := s.maxMembers()
	if kind == "direct" {
		cap = max(cap, 2)
	}
	if len(result) > cap {
		return nil, platform.ErrInvalid
	}
	return result, nil
}
