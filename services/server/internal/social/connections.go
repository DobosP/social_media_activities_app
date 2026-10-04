package social

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

const sharedActivity = `EXISTS(SELECT 1 FROM social_membership m JOIN social_membership peer ON peer.activity_id=m.activity_id WHERE m.user_id=$1 AND peer.user_id=$2 AND m.state='member' AND peer.state='member' AND m.role<>'guardian' AND peer.role<>'guardian')`
const sharedGroup = `EXISTS(SELECT 1 FROM social_groupmembership m JOIN social_groupmembership peer ON peer.group_id=m.group_id WHERE m.user_id=$1 AND peer.user_id=$2 AND m.state='member' AND peer.state='member')`
const connectionPair = `((requester_id=$1 AND addressee_id=$2) OR (requester_id=$2 AND addressee_id=$1))`

func pairVisible(a, b Actor) bool {
	return assigned(a) && assigned(b) && a.ID != b.ID && a.Cohort == b.Cohort
}
func (s *Service) canConnect(ctx context.Context, q platform.Querier, a, b Actor) error {
	if !pairVisible(a, b) || !s.ConnectionCohorts[a.Cohort] {
		return platform.ErrForbidden
	}
	if err := platform.Participate(ctx, q, a); err != nil {
		return err
	}
	if err := platform.Participate(ctx, q, b); err != nil {
		return err
	}
	blocked, err := platform.Blocked(ctx, q, a.ID, b.ID)
	if err := errorIfFalse(!blocked, err); err != nil {
		return err
	}
	yes, err := scalar(ctx, q, `SELECT `+sharedActivity, a.ID, b.ID)
	return errorIfFalse(yes, err)
}

const userRef = `jsonb_build_object('public_id',u.public_id,'display_name',COALESCE(NULLIF(u.display_name,''),u.username))`
const connectionProjection = `jsonb_build_object('id',c.id,'status',c.status,'requester',jsonb_build_object('public_id',req.public_id,'display_name',COALESCE(NULLIF(req.display_name,''),req.username)),'addressee',jsonb_build_object('public_id',ad.public_id,'display_name',COALESCE(NULLIF(ad.display_name,''),ad.username)),'created_at',c.created_at)`
const connectionJoin = ` FROM connections_connection c JOIN accounts_user req ON req.id=c.requester_id JOIN accounts_user ad ON ad.id=c.addressee_id `

func (s *Service) Connection(ctx context.Context, a Actor, id int64) (json.RawMessage, error) {
	return object(ctx, s.DB, `SELECT `+connectionProjection+connectionJoin+` WHERE c.id=$1 AND (c.requester_id=$2 OR c.addressee_id=$2)`, id, a.ID)
}
func (s *Service) connectionAudit(ctx context.Context, tx pgx.Tx, a Actor, event string, target int64) error {
	return s.Audit(ctx, tx, a, event, fmt.Sprintf("accounts.user:%d", target), nil)
}
func targetByPublicID(ctx context.Context, q platform.Querier, publicID string) (Actor, error) {
	var id int64
	if len(publicID) != 36 {
		return Actor{}, platform.ErrNotFound
	}
	err := q.QueryRow(ctx, `SELECT id FROM accounts_user WHERE public_id::text=$1`, publicID).Scan(&id)
	if err != nil {
		return Actor{}, err
	}
	return actorByID(ctx, q, id)
}
func pairLock(ctx context.Context, tx pgx.Tx, a, b int64) error {
	if a > b {
		a, b = b, a
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,701327))`, fmt.Sprintf("connection:%d:%d", a, b))
	return err
}
func (s *Service) RequestConnection(ctx context.Context, a Actor, publicID string) (int64, error) {
	var id int64
	err := s.rateTransaction(ctx, a, "connection_request", 20, time.Hour, func(tx pgx.Tx, reserve func() error) error {
		b, err := targetByPublicID(ctx, tx, publicID)
		if err != nil {
			return err
		}
		if err := pairLock(ctx, tx, a.ID, b.ID); err != nil {
			return err
		}
		if err := s.canConnect(ctx, tx, a, b); err != nil {
			return err
		}
		accepted, err := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM connections_connection WHERE `+connectionPair+` AND status='accepted')`, a.ID, b.ID)
		if err != nil {
			return err
		}
		if accepted {
			return platform.ErrInvalid
		}
		var existing int64
		err = tx.QueryRow(ctx, `SELECT id FROM connections_connection WHERE requester_id=$1 AND addressee_id=$2 AND status='pending'`, a.ID, b.ID).Scan(&existing)
		if err == nil {
			id = existing
			return nil
		}
		if err != pgx.ErrNoRows {
			return err
		}
		err = tx.QueryRow(ctx, `UPDATE connections_connection SET status='accepted',decided_at=now() WHERE requester_id=$2 AND addressee_id=$1 AND status='pending' RETURNING id`, a.ID, b.ID).Scan(&id)
		if err == nil {
			if err := s.notify(ctx, tx, b.ID, "connection_accepted", "Connection accepted", "", "/connections/"); err != nil {
				return err
			}
			return s.connectionAudit(ctx, tx, a, "connection.accepted", b.ID)
		}
		if err != pgx.ErrNoRows {
			return err
		}
		if err := reserve(); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO connections_connection(requester_id,addressee_id,status,created_at,decided_at) VALUES($1,$2,'pending',now(),NULL) ON CONFLICT(requester_id,addressee_id) DO UPDATE SET status='pending',decided_at=NULL RETURNING id`, a.ID, b.ID).Scan(&id)
		if err != nil {
			return err
		}
		if err := s.notify(ctx, tx, b.ID, "connection_request", a.DisplayName+" would like to connect", "", "/connections/"); err != nil {
			return err
		}
		return s.connectionAudit(ctx, tx, a, "connection.requested", b.ID)
	})
	return id, err
}
func (s *Service) RespondConnection(ctx context.Context, a Actor, id int64, action string) error {
	return s.privacyTransaction(ctx, a, func(tx pgx.Tx) error {
		var req, ad int64
		var status string
		if err := tx.QueryRow(ctx, `SELECT requester_id,addressee_id,status FROM connections_connection WHERE id=$1`, id).Scan(&req, &ad, &status); err != nil {
			return err
		}
		if err := pairLock(ctx, tx, req, ad); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT requester_id,addressee_id,status FROM connections_connection WHERE id=$1 FOR UPDATE`, id).Scan(&req, &ad, &status); err != nil {
			return err
		}
		if status != "pending" {
			return platform.ErrInvalid
		}
		next := "declined"
		target := req
		if action == "withdraw" {
			if req != a.ID {
				return platform.ErrForbidden
			}
			next = "withdrawn"
			target = ad
		} else {
			if ad != a.ID {
				return platform.ErrForbidden
			}
			if action == "accept" {
				b, err := actorByID(ctx, tx, req)
				if err != nil {
					return err
				}
				if err = s.canConnect(ctx, tx, a, b); err != nil {
					return err
				}
				next = "accepted"
			} else if action != "decline" {
				return platform.ErrInvalid
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE connections_connection SET status=$2,decided_at=now() WHERE id=$1`, id, next); err != nil {
			return err
		}
		if next == "accepted" {
			if err := s.notify(ctx, tx, req, "connection_accepted", "Connection accepted", "", "/connections/"); err != nil {
				return err
			}
		}
		return s.connectionAudit(ctx, tx, a, "connection."+next, target)
	})
}
func (s *Service) RemoveConnection(ctx context.Context, a Actor, publicID string) error {
	return s.privacyTransaction(ctx, a, func(tx pgx.Tx) error {
		b, err := targetByPublicID(ctx, tx, publicID)
		if err != nil {
			return err
		}
		if err := pairLock(ctx, tx, a.ID, b.ID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE connections_connection SET status='removed',decided_at=now() WHERE `+connectionPair+` AND status='accepted'`, a.ID, b.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			return s.connectionAudit(ctx, tx, a, "connection.removed", b.ID)
		}
		return nil
	})
}
func (s *Service) Connections(ctx context.Context, a Actor) ([]json.RawMessage, error) {
	if !assigned(a) {
		return []json.RawMessage{}, nil
	}
	return objects(ctx, s.DB, `SELECT `+userRef+` FROM accounts_user u WHERE u.is_active AND u.cohort=$2 AND u.id<>$1 AND EXISTS(SELECT 1 FROM connections_connection c WHERE c.status='accepted' AND ((c.requester_id=$1 AND c.addressee_id=u.id) OR (c.requester_id=u.id AND c.addressee_id=$1))) AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$1 AND b.blocked_id=u.id) OR (b.blocker_id=u.id AND b.blocked_id=$1)) ORDER BY u.display_name,u.username LIMIT 200`, a.ID, a.Cohort)
}
func (s *Service) PendingConnections(ctx context.Context, a Actor) (map[string][]json.RawMessage, error) {
	incoming, err := objects(ctx, s.DB, `SELECT `+connectionProjection+connectionJoin+` WHERE c.status='pending' AND c.addressee_id=$1 ORDER BY c.created_at DESC,c.id DESC LIMIT 200`, a.ID)
	if err != nil {
		return nil, err
	}
	outgoing, err := objects(ctx, s.DB, `SELECT `+connectionProjection+connectionJoin+` WHERE c.status='pending' AND c.requester_id=$1 ORDER BY c.created_at DESC,c.id DESC LIMIT 200`, a.ID)
	return map[string][]json.RawMessage{"incoming": incoming, "outgoing": outgoing}, err
}
func (s *Service) SearchConnections(ctx context.Context, a Actor, query string) ([]json.RawMessage, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) < 2 || !s.ConnectionCohorts[a.Cohort] {
		return []json.RawMessage{}, nil
	}
	return objects(ctx, s.DB, `SELECT `+userRef+` FROM accounts_user u WHERE u.id<>$1 AND u.cohort=$2 AND u.is_active AND (u.display_name ILIKE '%'||$3||'%' OR u.username ILIKE '%'||$3||'%') AND EXISTS(SELECT 1 FROM social_membership m JOIN social_membership peer ON peer.activity_id=m.activity_id WHERE m.user_id=$1 AND peer.user_id=u.id AND m.state='member' AND peer.state='member' AND m.role<>'guardian' AND peer.role<>'guardian') AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$1 AND b.blocked_id=u.id) OR (b.blocker_id=u.id AND b.blocked_id=$1)) AND NOT EXISTS(SELECT 1 FROM connections_connection c WHERE c.status IN ('pending','accepted') AND ((c.requester_id=$1 AND c.addressee_id=u.id) OR (c.requester_id=u.id AND c.addressee_id=$1))) ORDER BY u.id LIMIT 20`, a.ID, a.Cohort, escapeLike(query))
}

func (s *Service) Profile(ctx context.Context, a Actor, publicID string) (map[string]any, error) {
	if !s.allow(ctx, a.ID, "profile_card", 240, time.Hour) {
		return nil, platform.ErrForbidden
	}
	b, err := targetByPublicID(ctx, s.DB, publicID)
	if err != nil {
		return nil, err
	}
	if !pairVisible(a, b) {
		return nil, platform.ErrNotFound
	}
	blocked, err := platform.Blocked(ctx, s.DB, a.ID, b.ID)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, platform.ErrNotFound
	}
	var connected, sharedA, sharedG, joinRequest, pending bool
	err = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM connections_connection WHERE `+connectionPair+` AND status='accepted'),`+sharedActivity+`,`+sharedGroup+`,EXISTS(SELECT 1 FROM social_membership own JOIN social_membership req ON req.activity_id=own.activity_id WHERE own.state='member' AND own.role IN ('owner','co_organizer') AND req.state='requested' AND ((own.user_id=$1 AND req.user_id=$2) OR (own.user_id=$2 AND req.user_id=$1))),EXISTS(SELECT 1 FROM connections_connection WHERE `+connectionPair+` AND status='pending')`, a.ID, b.ID).Scan(&connected, &sharedA, &sharedG, &joinRequest, &pending)
	if err != nil {
		return nil, err
	}
	tier := "stranger"
	if connected {
		tier = "connected"
	} else if sharedA || sharedG || joinRequest {
		tier = "shared"
	}
	display := b.DisplayName
	if display == "" {
		display = "A member"
		if tier != "stranger" {
			display = b.Username
		}
	}
	if s.Avatar == nil {
		return nil, fmt.Errorf("signature avatar adapter unavailable")
	}
	avatar, err := s.Avatar(ctx, s.DB, b.ID)
	if err != nil {
		return nil, err
	}
	minor := a.Cohort == "child" || a.Cohort == "teen"
	card := map[string]any{"tier": tier, "public_id": b.PublicID, "display": display, "avatar": avatar, "minor": minor}
	if tier == "stranger" {
		return card, nil
	}
	activities, an, err := sharedTitles(ctx, s.DB, a.ID, b.ID, false)
	if err != nil {
		return nil, err
	}
	groups, gn, err := sharedTitles(ctx, s.DB, a.ID, b.ID, true)
	if err != nil {
		return nil, err
	}
	connectErr := s.canConnect(ctx, s.DB, a, b)
	if connectErr != nil && connectErr != platform.ErrForbidden {
		return nil, connectErr
	}
	card["username"] = b.Username
	card["verified"] = b.IdentityVerified
	card["shared"] = map[string]any{"activities": activities, "activity_count": an, "activity_overflow": max(0, an-len(activities)), "groups": groups, "group_count": gn, "group_overflow": max(0, gn-len(groups)), "join_request": joinRequest}
	card["connected"] = connected
	card["can_connect"] = !connected && connectErr == nil
	card["request_pending"] = !connected && pending
	card["can_message"] = connected
	card["interests"] = nil
	card["show_photo"] = false
	if connected && !minor {
		rows, err := s.DB.Query(ctx, `SELECT t.name FROM recommendations_userinterest i JOIN taxonomy_activitytype t ON t.id=i.activity_type_id WHERE i.user_id=$1 ORDER BY t.name LIMIT 200`, b.ID)
		if err != nil {
			return nil, err
		}
		interests := []string{}
		for rows.Next() {
			var label string
			if err := rows.Scan(&label); err != nil {
				rows.Close()
				return nil, err
			}
			interests = append(interests, label)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		card["interests"] = interests
		card["show_photo"] = true
	}
	return card, nil
}
func sharedTitles(ctx context.Context, q platform.Querier, a, b int64, groups bool) ([]string, int, error) {
	table, members, key := "social_activity", "social_membership", "activity_id"
	extra := ` AND m.role<>'guardian' AND peer.role<>'guardian'`
	if groups {
		table, members, key = "social_group", "social_groupmembership", "group_id"
		extra = ""
	}
	sql := `SELECT ob.title,COUNT(*) OVER() FROM ` + table + ` ob WHERE EXISTS(SELECT 1 FROM ` + members + ` m JOIN ` + members + ` peer ON peer.` + key + `=m.` + key + ` WHERE m.` + key + `=ob.id AND m.user_id=$1 AND peer.user_id=$2 AND m.state='member' AND peer.state='member'` + extra + `) ORDER BY ob.created_at DESC,ob.id DESC LIMIT 3`
	rows, err := q.Query(ctx, sql, a, b)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	labels := []string{}
	n := 0
	for rows.Next() {
		var label string
		if err := rows.Scan(&label, &n); err != nil {
			return nil, 0, err
		}
		labels = append(labels, label)
	}
	return labels, n, rows.Err()
}
