package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func EnsureSchema(ctx context.Context, db *pgxpool.Pool) error {
	_, e := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS messaging_go_ratebudget(user_id bigint NOT NULL REFERENCES accounts_user(id) ON DELETE CASCADE,action varchar(24) NOT NULL,window_start timestamptz NOT NULL,count integer NOT NULL CHECK(count>0),PRIMARY KEY(user_id,action))`)
	return e
}
func (s *Service) budget(ctx context.Context, tx pgx.Tx, user int64, action string, limit int) error {
	policy, err := budgets.Resolve(s.RatePolicies, action, budgets.Policy{Limit: limit, Window: time.Minute})
	if err != nil {
		return err
	}
	var allowed bool
	e := tx.QueryRow(ctx, `INSERT INTO messaging_go_ratebudget(user_id,action,window_start,count) VALUES($1,$2,now(),1) ON CONFLICT(user_id,action) DO UPDATE SET window_start=CASE WHEN messaging_go_ratebudget.window_start<=now()-$4::double precision*interval '1 second' THEN now() ELSE messaging_go_ratebudget.window_start END,count=CASE WHEN messaging_go_ratebudget.window_start<=now()-$4::double precision*interval '1 second' THEN 1 ELSE messaging_go_ratebudget.count+1 END WHERE messaging_go_ratebudget.window_start<=now()-$4::double precision*interval '1 second' OR messaging_go_ratebudget.count<$3 RETURNING true`, user, action, policy.Limit, policy.Window.Seconds()).Scan(&allowed)
	if errors.Is(e, pgx.ErrNoRows) {
		return platform.ErrForbidden
	}
	return e
}
func (s *Service) Start(ctx context.Context, a platform.Actor, kind string, names []string, title string) (result int64, err error) {
	if kind == "" {
		kind = "direct"
	}
	if kind != "direct" && kind != "group" || len(names) > 255 || len(names) == 0 || utf8.RuneCountInString(title) > 2000 {
		return 0, platform.ErrInvalid
	}
	title = trim(title, 120)
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		fresh, e := actor(ctx, tx, a.ID)
		if e != nil {
			return e
		}
		a = fresh
		if e = platform.Participate(ctx, tx, a); e != nil {
			return e
		}
		seen := map[int64]bool{}
		var targets []platform.Actor
		for _, name := range names {
			b, e := target(ctx, tx, name)
			if e != nil {
				return e
			}
			if kind == "group" && b.ID == a.ID {
				continue
			}
			if seen[b.ID] {
				continue
			}
			seen[b.ID] = true
			if e = pair(ctx, tx, a, b); e != nil {
				return e
			}
			targets = append(targets, b)
		}
		if len(targets) == 0 || kind == "group" && len(targets)+1 > s.maxMembers() || kind == "direct" && len(targets) != 1 {
			return platform.ErrInvalid
		}
		if kind == "direct" {
			if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("messaging-direct:%d:%d", min(a.ID, targets[0].ID), max(a.ID, targets[0].ID))); e != nil {
				return e
			}
			e = tx.QueryRow(ctx, `SELECT c.id FROM messaging_conversation c JOIN messaging_participant a ON a.conversation_id=c.id JOIN messaging_participant b ON b.conversation_id=c.id WHERE c.kind='direct' AND a.user_id=$1 AND b.user_id=$2 ORDER BY c.updated_at DESC,c.id DESC LIMIT 1`, a.ID, targets[0].ID).Scan(&result)
			if e == nil {
				// Re-invite a reused direct chat whose peer left or was removed
				// (for example by moderation). pair() above re-checked cohort,
				// participation and blocks; the peer must accept again. A
				// starter who left or was removed never re-enters on their own
				// while the peer is still active: the peer re-invites them.
				tag, e := tx.Exec(ctx, `UPDATE messaging_participant p SET state=CASE WHEN p.user_id=$2 THEN 'active' ELSE 'invited' END,invited_by_id=CASE WHEN p.user_id=$2 THEN p.invited_by_id ELSE $2 END WHERE p.conversation_id=$1 AND p.user_id IN($2,$3) AND p.state IN('left','removed') AND (p.user_id=$3 OR NOT EXISTS(SELECT 1 FROM messaging_participant o WHERE o.conversation_id=$1 AND o.user_id=$3 AND o.state='active'))`, result, a.ID, targets[0].ID)
				if e != nil {
					return e
				}
				if tag.RowsAffected() == 0 {
					return nil
				}
				if e = s.budget(ctx, tx, a.ID, "messaging_start", 20); e != nil {
					return e
				}
				return platform.RecordAudit(ctx, tx, a, "messaging.direct_reinvited", fmt.Sprintf("messaging.conversation:%d", result), map[string]any{"reinvited": tag.RowsAffected()})
			}
			if !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			title = ""
		}
		if e = s.budget(ctx, tx, a.ID, "messaging_start", 20); e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, `INSERT INTO messaging_conversation(kind,title,cohort,disappearing_seconds,creator_id,created_at,updated_at) VALUES($1,$2,$3,0,$4,now(),now()) RETURNING id`, kind, title, a.Cohort, a.ID).Scan(&result); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO messaging_participant(conversation_id,user_id,state,role,invited_by_id,created_at,joined_at,last_read_at) VALUES($1,$2,'active','admin',NULL,now(),now(),NULL)`, result, a.ID); e != nil {
			return e
		}
		for _, b := range targets {
			if _, e = tx.Exec(ctx, `INSERT INTO messaging_participant(conversation_id,user_id,state,role,invited_by_id,created_at,joined_at,last_read_at) VALUES($1,$2,'invited','member',$3,now(),NULL,NULL)`, result, b.ID, a.ID); e != nil {
				return e
			}
		}
		return platform.RecordAudit(ctx, tx, a, "messaging."+kind+"_started", fmt.Sprintf("messaging.conversation:%d", result), map[string]any{"invited": len(targets)})
	})
	return result, err
}
func trim(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// addUserAvatars keeps the shared bounded avatar projection after roster/sender
// identities have been read with their owning rows. Rows must already be closed.
func addUserAvatars(ctx context.Context, q platform.Querier, ids []int64, refs map[int64]any) error {
	avatars, err := accounts.Avatars(ctx, q, ids)
	if err != nil {
		return err
	}
	for id, ref := range refs {
		ref.(map[string]any)["avatar"] = avatars[id]
	}
	return nil
}

func scanConversations(rows pgx.Rows) ([]int64, map[int64]map[string]any, error) {
	defer rows.Close()
	ids := []int64{}
	out := map[int64]map[string]any{}
	for rows.Next() {
		var id int64
		var kind, title, cohort string
		var myState, myRole *string
		var disappearing int
		var created, updated time.Time
		if err := rows.Scan(&id, &kind, &title, &cohort, &disappearing, &created, &updated, &myState, &myRole); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		out[id] = map[string]any{"id": id, "kind": kind, "title": title, "cohort": cohort, "disappearing_seconds": disappearing, "created_at": created, "updated_at": updated, "my_state": myState, "my_role": myRole, "participants": []any{}}
	}
	return ids, out, rows.Err()
}

func (s *Service) populateConversationParticipants(ctx context.Context, q platform.Querier, ids []int64, out map[int64]map[string]any) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.Query(ctx, `SELECT p.conversation_id,p.user_id,p.state,p.role,p.joined_at,u.public_id::text,u.username,u.display_name FROM messaging_participant p LEFT JOIN accounts_user u ON u.id=p.user_id WHERE p.conversation_id=ANY($1) AND p.state IN('active','invited') ORDER BY p.id`, ids)
	if err != nil {
		return err
	}
	type part struct {
		conversation, user int64
		state, role        string
		joined             *time.Time
	}
	var parts []part
	users := map[int64]bool{}
	refs := map[int64]any{}
	counts := map[int64]int{}
	for rows.Next() {
		var p part
		var publicID, username, display *string
		if err = rows.Scan(&p.conversation, &p.user, &p.state, &p.role, &p.joined, &publicID, &username, &display); err != nil {
			rows.Close()
			return err
		}
		conv := out[p.conversation]
		if conv == nil {
			rows.Close()
			return platform.ErrInvalid
		}
		counts[p.conversation]++
		cap := s.maxMembers()
		if conv["kind"] == "direct" {
			cap = max(cap, 2)
		}
		if counts[p.conversation] > cap {
			rows.Close()
			return platform.ErrInvalid
		}
		parts = append(parts, p)
		users[p.user] = true
		if publicID != nil && username != nil && display != nil {
			refs[p.user] = map[string]any{"public_id": *publicID, "username": *username, "display_name": *display}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	userIDs := make([]int64, 0, len(users))
	for id := range users {
		userIDs = append(userIDs, id)
	}
	if err = addUserAvatars(ctx, q, userIDs, refs); err != nil {
		return err
	}
	for _, p := range parts {
		conv := out[p.conversation]
		conv["participants"] = append(conv["participants"].([]any), map[string]any{"user": refs[p.user], "state": p.state, "role": p.role, "joined_at": p.joined})
	}
	return nil
}

func (s *Service) serializeConversations(ctx context.Context, q platform.Querier, a platform.Actor, ids []int64) (map[int64]map[string]any, error) {
	out := map[int64]map[string]any{}
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > 100 {
		return nil, platform.ErrInvalid
	}
	rows, err := q.Query(ctx, `SELECT c.id,c.kind,c.title,c.cohort,c.disappearing_seconds,c.created_at,c.updated_at,p.state,p.role FROM messaging_conversation c LEFT JOIN messaging_participant p ON p.conversation_id=c.id AND p.user_id=$2 WHERE c.id=ANY($1)`, ids, a.ID)
	if err != nil {
		return nil, err
	}
	_, out, err = scanConversations(rows)
	if err != nil {
		return nil, err
	}
	if err = s.populateConversationParticipants(ctx, q, ids, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) conversation(ctx context.Context, q platform.Querier, a platform.Actor, id int64) (map[string]any, error) {
	var owned bool
	e := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2)`, id, a.ID).Scan(&owned)
	if e != nil {
		return nil, e
	}
	if !owned {
		eligible, e := s.guardianEligible(ctx, q, a, id)
		if e != nil {
			return nil, e
		}
		if !eligible {
			return nil, platform.ErrForbidden
		}
	}
	rows, e := s.serializeConversations(ctx, q, a, []int64{id})
	if e != nil {
		return nil, e
	}
	if rows[id] == nil {
		return nil, pgx.ErrNoRows
	}
	return rows[id], nil
}
func (s *Service) Conversation(ctx context.Context, a platform.Actor, id int64) (map[string]any, error) {
	return s.conversation(ctx, s.DB, a, id)
}
func (s *Service) Conversations(ctx context.Context, a platform.Actor, query string, limit, offset int, guardian bool, includePending ...bool) ([]map[string]any, bool, error) {
	if len(includePending) > 1 {
		return nil, false, platform.ErrInvalid
	}
	pending := true
	if len(includePending) == 1 {
		pending = includePending[0]
	}
	a, e := actor(ctx, s.DB, a.ID)
	if e != nil {
		return nil, false, e
	}
	if !a.IsActive {
		return nil, false, platform.ErrForbidden
	}
	if limit < 1 || limit > 100 {
		limit = 100
	}
	if offset < 0 || offset > 1_000_000 {
		return nil, false, platform.ErrInvalid
	}
	if utf8.RuneCountInString(query) < 2 {
		query = ""
	}
	query = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(query, "\\", "\\\\"), "%", "\\%"), "_", "\\_")
	filter := `EXISTS(SELECT 1 FROM messaging_participant mine WHERE mine.conversation_id=c.id AND mine.user_id=$1 AND mine.state IN('active','invited'))`
	if !pending {
		filter = `EXISTS(SELECT 1 FROM messaging_participant mine WHERE mine.conversation_id=c.id AND mine.user_id=$1 AND mine.state='active')`
	}
	if guardian {
		if a.Cohort != "adult" {
			return nil, false, platform.ErrForbidden
		}
		if e = platform.Participate(ctx, s.DB, a); e != nil {
			return nil, false, e
		}
		filter = `EXISTS(SELECT 1 FROM messaging_participant p JOIN accounts_guardianrelationship g ON g.ward_id=p.user_id JOIN accounts_user u ON u.id=p.user_id WHERE p.conversation_id=c.id AND p.state='active' AND u.cohort='child' AND u.is_active AND g.guardian_id=$1 AND g.status='active')`
	}
	rows, e := s.DB.Query(ctx, `SELECT c.id,c.kind,c.title,c.cohort,c.disappearing_seconds,c.created_at,c.updated_at,mine.state,mine.role FROM messaging_conversation c LEFT JOIN messaging_participant mine ON mine.conversation_id=c.id AND mine.user_id=$1 WHERE `+filter+` AND ($2='' OR c.title ILIKE '%'||$2||'%' OR EXISTS(SELECT 1 FROM messaging_participant p JOIN accounts_user u ON u.id=p.user_id WHERE p.conversation_id=c.id AND p.state IN('active','invited') AND (u.username ILIKE '%'||$2||'%' OR u.display_name ILIKE '%'||$2||'%'))) ORDER BY c.updated_at DESC,c.id DESC LIMIT $3 OFFSET $4`, a.ID, query, limit+1, offset)
	if e != nil {
		return nil, false, e
	}
	ids, serialized, e := scanConversations(rows)
	if e != nil {
		return nil, false, e
	}
	next := len(ids) > limit
	if next {
		delete(serialized, ids[limit])
		ids = ids[:limit]
	}
	if e = s.populateConversationParticipants(ctx, s.DB, ids, serialized); e != nil {
		return nil, false, e
	}
	result := []map[string]any{}
	for _, id := range ids {
		result = append(result, serialized[id])
	}
	return result, next, nil
}

// PruneObservers is shared with account/guardian revocation paths. Each removed
// observer is audited; a cross-cohort adult cannot outlive their active CHILD ward.
func (s *Service) PruneObservers(ctx context.Context, tx pgx.Tx, conversation int64) error {
	rows, e := tx.Query(ctx, `UPDATE messaging_participant gp SET state='removed' WHERE gp.conversation_id=$1 AND gp.role='guardian' AND gp.state='active' AND NOT EXISTS(SELECT 1 FROM messaging_participant p JOIN accounts_guardianrelationship g ON g.ward_id=p.user_id JOIN accounts_user u ON u.id=p.user_id WHERE p.conversation_id=gp.conversation_id AND p.state='active' AND p.role!='guardian' AND u.cohort='child' AND u.is_active AND g.guardian_id=gp.user_id AND g.status='active') RETURNING gp.user_id`, conversation)
	if e != nil {
		return e
	}
	var users []int64
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		users = append(users, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range users {
		if e = platform.RecordAudit(ctx, tx, platform.Actor{ID: id}, "messaging.guardian_observer_ended", "", map[string]any{"conversation_id": conversation}); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) RemoveUser(ctx context.Context, tx pgx.Tx, userID int64, reason string) error {
	rows, e := tx.Query(ctx, `UPDATE messaging_participant SET state='removed' WHERE user_id=$1 AND state IN('active','invited') RETURNING conversation_id`, userID)
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
	for _, id := range ids {
		if e = s.PruneObservers(ctx, tx, id); e != nil {
			return e
		}
	}
	if len(ids) > 0 {
		return platform.RecordAudit(ctx, tx, platform.Actor{ID: userID}, "messaging.participation_revoked", "", map[string]any{"count": len(ids), "reason": reason})
	}
	return nil
}

// Keep JSON serialization concrete at the boundary, without storing payloads
// in notification/audit metadata.
func messageJSON(value any) (json.RawMessage, error) { return json.Marshal(value) }
