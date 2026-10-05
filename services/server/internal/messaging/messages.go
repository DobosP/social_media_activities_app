package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/chat"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

type RecipientKey struct {
	RecipientPublicID  string         `json:"recipient_public_id"`
	EphemeralPublicJWK map[string]any `json:"ephemeral_public_jwk"`
	WrappedKey         string         `json:"wrapped_key"`
	WrapIV             string         `json:"wrap_iv"`
}
type MessageInput struct {
	Ciphertext    string         `json:"ciphertext"`
	IV            string         `json:"iv"`
	Algorithm     string         `json:"algorithm"`
	RecipientKeys []RecipientKey `json:"recipient_keys"`
}

func publicJWK(jwk map[string]any) bool {
	if jwk == nil || len(jwk) > 12 {
		return false
	}
	kind, ok := jwk["kty"].(string)
	if !ok || kind != "EC" && kind != "OKP" {
		return false
	}
	x, ok := jwk["x"].(string)
	if !ok || x == "" || len(x) > 512 {
		return false
	}
	for key, value := range jwk {
		switch key {
		case "kty", "x", "y", "crv", "alg", "kid", "use":
			text, ok := value.(string)
			if !ok || len(text) > 512 {
				return false
			}
		case "ext":
			if _, ok := value.(bool); !ok {
				return false
			}
		case "key_ops":
			switch ops := value.(type) {
			case []any:
				if len(ops) > 8 {
					return false
				}
				for _, op := range ops {
					if text, ok := op.(string); !ok || len(text) > 32 {
						return false
					}
				}
			case []string:
				if len(ops) > 8 {
					return false
				}
			default:
				return false
			}
		default:
			return false
		}
	}
	return true
}
func validateInput(in *MessageInput, maxMembers int) error {
	return validateInputBounded(in, maxMembers, 65536)
}
func validateInputBounded(in *MessageInput, maxMembers, maxCiphertext int) error {
	if in.Ciphertext == "" || len(in.Ciphertext) > maxCiphertext || in.IV == "" || len(in.IV) > 64 || len(in.RecipientKeys) > maxMembers || len(in.Algorithm) > 32 {
		return platform.ErrInvalid
	}
	if in.Algorithm == "" {
		in.Algorithm = "AES-GCM-256"
	}
	return nil
}
func (s *Service) Post(ctx context.Context, a platform.Actor, conversation int64, input MessageInput) (result int64, err error) {
	if err = validateInputBounded(&input, s.maxMembers(), s.maxCiphertext()); err != nil {
		return 0, err
	}
	// Admission commits independently so malformed recipient sets still consume
	// their cheap per-sender budget, as the reference's atomic cache limiter did.
	if err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if e := s.canWrite(ctx, tx, a, conversation); e != nil {
			return e
		}
		return s.budget(ctx, tx, a.ID, "messaging_send", 60)
	}); err != nil {
		return 0, err
	}
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `SELECT id FROM messaging_conversation WHERE id=$1 FOR UPDATE`, conversation); e != nil {
			return e
		}
		fresh, e := actor(ctx, tx, a.ID)
		if e != nil {
			return e
		}
		a = fresh
		if e = s.canWrite(ctx, tx, a, conversation); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `SELECT u.id,u.public_id::text,p.role FROM messaging_participant p JOIN accounts_user u ON u.id=p.user_id WHERE p.conversation_id=$1 AND p.state='active' ORDER BY p.id LIMIT 257`, conversation)
		if e != nil {
			return e
		}
		type member struct {
			id        int64
			pub, role string
		}
		var members []member
		for rows.Next() {
			var m member
			if e = rows.Scan(&m.id, &m.pub, &m.role); e != nil {
				rows.Close()
				return e
			}
			members = append(members, m)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(members) > s.maxMembers() || len(members) != len(input.RecipientKeys) {
			return platform.ErrInvalid
		}
		active := map[string]int64{}
		for _, m := range members {
			if m.role == "guardian" {
				guardian, e := actor(ctx, tx, m.id)
				if e != nil {
					return e
				}
				eligible, e := s.guardianEligible(ctx, tx, guardian, conversation)
				if e != nil {
					return e
				}
				if !eligible {
					return platform.ErrForbidden
				}
			} else if m.id != a.ID {
				peer, e := actor(ctx, tx, m.id)
				if e != nil {
					return e
				}
				if e = pair(ctx, tx, a, peer); e != nil {
					return e
				}
			}
			active[m.pub] = m.id
		}
		seen := map[string]bool{}
		for _, key := range input.RecipientKeys {
			if seen[key.RecipientPublicID] || active[key.RecipientPublicID] == 0 || !publicJWK(key.EphemeralPublicJWK) || key.WrappedKey == "" || len(key.WrappedKey) > 4096 || key.WrapIV == "" || len(key.WrapIV) > 64 {
				return platform.ErrInvalid
			}
			seen[key.RecipientPublicID] = true
		}
		if len(seen) != len(active) {
			return platform.ErrInvalid
		}
		if e = tx.QueryRow(ctx, `INSERT INTO messaging_message(conversation_id,sender_id,algorithm,ciphertext,iv,created_at) VALUES($1,$2,$3,$4,$5,now()) RETURNING id`, conversation, a.ID, input.Algorithm, input.Ciphertext, input.IV).Scan(&result); e != nil {
			return e
		}
		for _, key := range input.RecipientKeys {
			raw, e := json.Marshal(key.EphemeralPublicJWK)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `INSERT INTO messaging_messagekey(message_id,recipient_id,ephemeral_public_jwk,wrapped_key,wrap_iv,created_at) VALUES($1,$2,$3,$4,$5,now())`, result, active[key.RecipientPublicID], raw, key.WrappedKey, key.WrapIV); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(ctx, `UPDATE messaging_conversation SET updated_at=now() WHERE id=$1`, conversation); e != nil {
			return e
		}
		if e = platform.RecordAudit(ctx, tx, a, "messaging.message_sent", "", map[string]any{"conversation_id": conversation, "recipients": len(active)}); e != nil {
			return e
		}
		return chat.Publish(ctx, tx, chat.Event{Kind: "messaging", Event: "message", RoomID: conversation, MessageID: result})
	})
	return result, err
}

type messageRow struct {
	id, conversation          int64
	sender                    *int64
	algorithm, ciphertext, iv string
	created                   time.Time
	key                       any
}

const messageReadProjection = `SELECT m.id,m.conversation_id,m.sender_id,m.algorithm,m.ciphertext,m.iv,m.created_at,k.ephemeral_public_jwk,k.wrapped_key,k.wrap_iv,u.public_id::text,u.username,u.display_name FROM messaging_message m JOIN messaging_conversation c ON c.id=m.conversation_id JOIN messaging_messagekey k ON k.message_id=m.id AND k.recipient_id=$2 LEFT JOIN accounts_user u ON u.id=m.sender_id`

func (s *Service) messageRows(ctx context.Context, q platform.Querier, a platform.Actor, ids []int64, broadcast bool) (map[int64]map[string]any, error) {
	if len(ids) == 0 {
		return map[int64]map[string]any{}, nil
	}
	if len(ids) > 51 {
		return nil, platform.ErrInvalid
	}
	rows, err := q.Query(ctx, messageReadProjection+` WHERE m.id=ANY($1) AND (c.disappearing_seconds=0 OR m.created_at>now()-c.disappearing_seconds*interval '1 second') ORDER BY m.created_at,m.id`, ids, a.ID)
	if err != nil {
		return nil, err
	}
	_, out, err := s.serializeMessageRows(ctx, q, rows, broadcast)
	return out, err
}

func (s *Service) serializeMessageRows(ctx context.Context, q platform.Querier, rows pgx.Rows, broadcast bool) ([]int64, map[int64]map[string]any, error) {
	out := map[int64]map[string]any{}
	ids := []int64{}
	var messages []messageRow
	senderIDs := map[int64]bool{}
	refs := map[int64]any{}
	for rows.Next() {
		var m messageRow
		var jwk json.RawMessage
		var wrapped, iv string
		var publicID, username, display *string
		if err := rows.Scan(&m.id, &m.conversation, &m.sender, &m.algorithm, &m.ciphertext, &m.iv, &m.created, &jwk, &wrapped, &iv, &publicID, &username, &display); err != nil {
			rows.Close()
			return nil, nil, err
		}
		m.key = map[string]any{"ephemeral_public_jwk": jwk, "wrapped_key": wrapped, "wrap_iv": iv}
		messages = append(messages, m)
		ids = append(ids, m.id)
		if m.sender != nil {
			senderIDs[*m.sender] = true
			if publicID != nil && username != nil && display != nil {
				refs[*m.sender] = map[string]any{"public_id": *publicID, "username": *username, "display_name": *display}
			}
		}
	}
	err := rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	senders := make([]int64, 0, len(senderIDs))
	for id := range senderIDs {
		senders = append(senders, id)
	}
	if err = addUserAvatars(ctx, q, senders, refs); err != nil {
		return nil, nil, err
	}
	for _, m := range messages {
		var sender any
		if m.sender != nil {
			sender = refs[*m.sender]
		}
		item := map[string]any{"id": m.id, "conversation": m.conversation, "sender": sender, "algorithm": m.algorithm, "ciphertext": m.ciphertext, "iv": m.iv, "created_at": m.created}
		if !broadcast {
			item["key"] = m.key
		}
		out[m.id] = item
	}
	if broadcast && len(ids) > 0 {
		rows, err = q.Query(ctx, `SELECT k.message_id,u.public_id::text,k.ephemeral_public_jwk,k.wrapped_key,k.wrap_iv FROM messaging_messagekey k JOIN accounts_user u ON u.id=k.recipient_id WHERE k.message_id=ANY($1) ORDER BY k.id`, ids)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var id int64
			var publicID, wrapped, iv string
			var jwk json.RawMessage
			if err = rows.Scan(&id, &publicID, &jwk, &wrapped, &iv); err != nil {
				rows.Close()
				return nil, nil, err
			}
			item := out[id]
			if item != nil {
				keys, ok := item["keys"].([]any)
				if !ok {
					keys = []any{}
				}
				item["keys"] = append(keys, map[string]any{"recipient_public_id": publicID, "ephemeral_public_jwk": jwk, "wrapped_key": wrapped, "wrap_iv": iv})
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, nil, err
		}
	}
	return ids, out, nil
}
func (s *Service) Message(ctx context.Context, a platform.Actor, id int64, broadcast bool) (map[string]any, error) {
	var conversation int64
	if e := s.DB.QueryRow(ctx, `SELECT conversation_id FROM messaging_message WHERE id=$1`, id).Scan(&conversation); e != nil {
		return nil, e
	}
	ok, e := s.CanView(ctx, s.DB, a, conversation)
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, platform.ErrForbidden
	}
	items, e := s.messageRows(ctx, s.DB, a, []int64{id}, broadcast)
	if e != nil {
		return nil, e
	}
	if items[id] == nil {
		return nil, pgx.ErrNoRows
	}
	return items[id], nil
}
func (s *Service) message(ctx context.Context, q platform.Querier, a platform.Actor, id int64, broadcast bool) (map[string]any, error) {
	if q != s.DB {
		return nil, platform.ErrInvalid
	}
	return s.Message(ctx, a, id, broadcast)
}
func (s *Service) Messages(ctx context.Context, a platform.Actor, conversation int64, limit int, after, before int64) ([]any, error) {
	ok, e := s.CanView(ctx, s.DB, a, conversation)
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, platform.ErrForbidden
	}
	if limit < 1 || limit > 51 {
		return nil, platform.ErrInvalid
	}
	order := "DESC"
	condition := `($3::bigint<=0 OR NOT EXISTS(SELECT 1 FROM messaging_message anchor WHERE anchor.id=$3 AND anchor.conversation_id=$1) OR (m.created_at,m.id)<(SELECT anchor.created_at,anchor.id FROM messaging_message anchor WHERE anchor.id=$3 AND anchor.conversation_id=$1))`
	if before <= 0 && after > 0 {
		condition = `m.id>$3`
		order = "ASC"
		before = after
	}
	rows, e := s.DB.Query(ctx, messageReadProjection+` WHERE m.conversation_id=$1 AND `+condition+` AND (c.disappearing_seconds=0 OR m.created_at>now()-c.disappearing_seconds*interval '1 second') ORDER BY m.created_at `+order+`,m.id `+order+` LIMIT $4`, conversation, a.ID, before, limit)
	if e != nil {
		return nil, e
	}
	ids, items, e := s.serializeMessageRows(ctx, s.DB, rows, false)
	if e != nil {
		return nil, e
	}
	if order == "DESC" {
		for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
			ids[i], ids[j] = ids[j], ids[i]
		}
	}
	result := []any{}
	for _, id := range ids {
		if item := items[id]; item != nil {
			result = append(result, item)
		}
	}
	return result, nil
}

func (s *Service) PurgeExpired(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 10000 || s.RetentionDays < 0 {
		return 0, platform.ErrInvalid
	}
	removed := 0
	e := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT m.id FROM messaging_message m JOIN messaging_conversation c ON c.id=m.conversation_id WHERE (c.disappearing_seconds>0 AND m.created_at<now()-c.disappearing_seconds*interval '1 second') OR ($1::int>0 AND m.created_at<now()-$1*interval '1 day') ORDER BY m.created_at,m.id LIMIT $2 FOR UPDATE OF m SKIP LOCKED`, s.RetentionDays, limit)
		if e != nil {
			return e
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(ids) == 0 {
			return nil
		}
		if _, e = tx.Exec(ctx, `DELETE FROM messaging_messagekey WHERE message_id=ANY($1)`, ids); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `DELETE FROM messaging_message WHERE id=ANY($1)`, ids); e != nil {
			return e
		}
		removed = len(ids)
		return platform.RecordAudit(ctx, tx, platform.Actor{}, "messaging.retention_purged", "", map[string]any{"count": removed})
	})
	return removed, e
}
func (s *Service) Report(ctx context.Context, a platform.Actor, conversation, id int64, reason, detail, excerpt string) (int64, error) {
	if reason == "" {
		reason = "other"
	}
	if !map[string]bool{"grooming": true, "harassment": true, "csam": true, "spam": true, "off_platform": true, "other": true}[reason] || len(detail) > 64<<10 || len(excerpt) > 64<<10 {
		return 0, platform.ErrInvalid
	}
	excerpt = trim(excerpt, 2000)
	detail = strings.TrimSpace(detail)
	var report int64
	e := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		// Reporting is not reading: an active seat on an active account is the
		// whole gate (source is_active_participant). A block with any peer, lapsed
		// participation or guardian eligibility never removes the evidence path.
		var ok bool
		e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM messaging_participant p JOIN accounts_user u ON u.id=p.user_id AND u.is_active WHERE p.conversation_id=$1 AND p.user_id=$2 AND p.state='active')`, conversation, a.ID).Scan(&ok)
		if e != nil {
			return e
		}
		if !ok {
			return platform.ErrInvalid
		}
		var sender *int64
		if e = tx.QueryRow(ctx, `SELECT sender_id FROM messaging_message WHERE id=$1 AND conversation_id=$2`, id, conversation).Scan(&sender); e != nil {
			return e
		}
		senderText := "None"
		if sender != nil {
			senderText = fmt.Sprint(*sender)
		}
		if excerpt == "" {
			excerpt = "(none provided)"
		}
		note := fmt.Sprintf("E2EE message report. conversation=%d message=%d sender=%s.\nReporter-decrypted content:\n%s", conversation, id, senderText, excerpt)
		if detail != "" {
			note = detail + "\n\n" + note
		}
		if e = tx.QueryRow(ctx, `INSERT INTO safety_report(reporter_id,target_type_id,target_id,reason,detail,status,handled_by_id,handled_at,resolution,created_at) VALUES($1,(SELECT id FROM django_content_type WHERE app_label='messaging' AND model='message'),$2,$3,$4,'open',NULL,NULL,'',now()) RETURNING id`, a.ID, id, reason, note).Scan(&report); e != nil {
			return e
		}
		if e = platform.RecordAudit(ctx, tx, a, "report.filed", fmt.Sprintf("messaging.message:%d", id), map[string]any{"reason": reason}); e != nil {
			return e
		}
		if _, e = platform.Notify(ctx, tx, a.ID, "moderation", "We received your report", "Thanks - your report was sent to the moderation team. We'll let you know once it's been reviewed.", "/settings/privacy/"); e != nil {
			return e
		}
		return platform.RecordAudit(ctx, tx, a, "messaging.message_reported", fmt.Sprintf("messaging.message:%d", id), map[string]any{"reason": reason})
	})
	return report, e
}
func (s *Service) transition(ctx context.Context, a platform.Actor, id int64, action string) error {
	return s.Transition(ctx, a, id, action)
}
