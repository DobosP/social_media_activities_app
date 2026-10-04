package social

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/chat"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

type threadState struct {
	ID, ThreadID, OwnerID       int64
	Kind, Cohort, Status, Title string
}

func threadOwner(ctx context.Context, q platform.Querier, a Actor, kind string, id int64, lock bool) (threadState, error) {
	if kind == "activity" {
		v, err := activity(ctx, q, a, id, lock)
		return threadState{v.ID, v.ThreadID, v.OwnerID, "activity", v.Cohort, v.Status, v.Title}, err
	}
	if kind != "group" {
		return threadState{}, platform.ErrNotFound
	}
	v, err := group(ctx, q, a, id, lock, false)
	return threadState{v.ID, v.ThreadID, v.OwnerID, "group", v.Cohort, v.Status, v.Title}, err
}
func memberRole(ctx context.Context, q platform.Querier, a Actor, v threadState) (string, error) {
	sql := `SELECT role FROM social_membership WHERE activity_id=$1 AND user_id=$2 AND state='member'`
	if v.Kind == "group" {
		sql = `SELECT role FROM social_groupmembership WHERE group_id=$1 AND user_id=$2 AND state='member'`
	}
	var role string
	err := q.QueryRow(ctx, sql, v.ID, a.ID).Scan(&role)
	return role, err
}
func threadGate(ctx context.Context, q platform.Querier, a Actor, v threadState, write bool) error {
	if !assigned(a) || a.Cohort != v.Cohort || v.ThreadID <= 0 {
		return platform.ErrForbidden
	}
	if err := platform.Participate(ctx, q, a); err != nil {
		return err
	}
	role, err := memberRole(ctx, q, a, v)
	if err == pgx.ErrNoRows {
		return platform.ErrForbidden
	}
	if err != nil {
		return err
	}
	blocked, err := platform.Blocked(ctx, q, a.ID, v.OwnerID)
	if err := errorIfFalse(!blocked, err); err != nil {
		return err
	}
	if write {
		if role == "guardian" || v.Kind == "group" && v.Cohort != "adult" {
			return platform.ErrForbidden
		}
		if v.Kind == "activity" && v.Status == "cancelled" || v.Kind == "group" && v.Status != "active" {
			return platform.ErrInvalid
		}
	}
	return nil
}

func sentimentGate(ctx context.Context, q platform.Querier, a Actor, v threadState) error {
	if err := threadGate(ctx, q, a, v, false); err != nil {
		return err
	}
	role, err := memberRole(ctx, q, a, v)
	if err != nil {
		return err
	}
	if role == "guardian" {
		return platform.ErrForbidden
	}
	if v.Kind == "activity" && v.Status == "cancelled" || v.Kind == "group" && v.Status != "active" {
		return platform.ErrInvalid
	}
	return nil
}

func postProjection(ctx context.Context) string {
	safePlace := catalog.PolicyFromContext(ctx).PlaceSQL()

	postShare := `CASE WHEN po.shared_activity_id IS NOT NULL THEN CASE WHEN sa.id IS NOT NULL AND NOT sa.is_hidden AND sa.status<>'cancelled' AND sa.cohort=$2 THEN jsonb_build_object('kind','activity','id',sa.id,'title',sa.title) ELSE jsonb_build_object('kind','gone') END WHEN po.shared_place_id IS NOT NULL THEN CASE WHEN sp.id IS NOT NULL AND (` + regexp.MustCompile(`\bp\.`).ReplaceAllString(safePlace, "sp.") + `) THEN jsonb_build_object('kind','place','id',sp.id,'title',sp.name) ELSE jsonb_build_object('kind','gone') END WHEN po.shared_event_id IS NOT NULL THEN CASE WHEN se.id IS NOT NULL AND NOT se.is_tombstone AND NOT se.is_import_held AND se.lifecycle_status IN ('scheduled','rescheduled','sold_out') AND (se.place_id IS NULL OR EXISTS(SELECT 1 FROM places_place p WHERE p.id=se.place_id AND ` + safePlace + `)) THEN jsonb_build_object('kind','event','id',se.id,'title',se.title) ELSE jsonb_build_object('kind','gone') END ELSE NULL END`
	return `jsonb_build_object('id',po.id,'author',u.display_name,'body',po.body,'is_announcement',po.is_announcement,'reply_to',po.reply_to_id,'share',` + postShare + `,'created_at',po.created_at)`

}

const postJoin = ` FROM social_post po JOIN accounts_user u ON u.id=po.author_id LEFT JOIN social_activity sa ON sa.id=po.shared_activity_id LEFT JOIN places_place sp ON sp.id=po.shared_place_id LEFT JOIN events_event se ON se.id=po.shared_event_id `

func (s *Service) Posts(ctx context.Context, a Actor, kind string, id, before int64, limit int) ([]json.RawMessage, string, error) {
	v, err := threadOwner(ctx, s.DB, a, kind, id, false)
	if err != nil {
		return nil, "", err
	}
	if err := threadGate(ctx, s.DB, a, v, false); err != nil {
		return nil, "", err
	}
	if limit < 1 {
		limit = s.Policy.ThreadPostLimit
	}
	if limit > s.Policy.ThreadPostLimit {
		limit = s.Policy.ThreadPostLimit
	}
	query := `SELECT ` + postProjection(ctx) + postJoin + ` WHERE po.thread_id=$1 AND NOT po.is_hidden AND ($3::bigint<=0 OR NOT EXISTS(SELECT 1 FROM social_post anchor WHERE anchor.id=$3 AND anchor.thread_id=$1) OR (po.created_at,po.id)<(SELECT anchor.created_at,anchor.id FROM social_post anchor WHERE anchor.id=$3 AND anchor.thread_id=$1)) ORDER BY po.created_at DESC,po.id DESC LIMIT $4`
	rows, err := objects(ctx, s.DB, query, v.ThreadID, a.Cohort, before, limit+1)
	if err != nil {
		return nil, "", err
	}
	cursor := ""
	if len(rows) > limit {
		rows = rows[:limit]
		var last struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(rows[len(rows)-1], &last); err != nil {
			return nil, "", err
		}
		cursor = fmt.Sprint(last.ID)
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows, cursor, nil
}
func (s *Service) Post(ctx context.Context, a Actor, id int64) (json.RawMessage, error) {
	var threadID, activityID, groupID int64
	if err := s.DB.QueryRow(ctx, `SELECT th.id,COALESCE(th.activity_id,0),COALESCE(th.group_id,0) FROM social_post p JOIN social_thread th ON th.id=p.thread_id WHERE p.id=$1`, id).Scan(&threadID, &activityID, &groupID); err != nil {
		return nil, err
	}
	kind, ownerID := "activity", activityID
	if groupID > 0 {
		kind, ownerID = "group", groupID
	}
	v, err := threadOwner(ctx, s.DB, a, kind, ownerID, false)
	if kind == "group" && a.IsStaff {
		g, e := group(ctx, s.DB, a, ownerID, false, true)
		err = e
		v = threadState{g.ID, g.ThreadID, g.OwnerID, "group", g.Cohort, g.Status, g.Title}
	}
	if err != nil {
		return nil, err
	}
	if err := threadGate(ctx, s.DB, a, v, false); err != nil {
		// A staff curator may receive their own announcement result while the
		// minor peer history stays unreadable. No other author's post is exposed.
		if kind != "group" || !a.IsStaff {
			return nil, err
		}
		yes, e := scalar(ctx, s.DB, `SELECT EXISTS(SELECT 1 FROM social_post po JOIN social_group g ON g.id=$2 WHERE po.id=$1 AND po.author_id=$3 AND po.is_announcement AND NOT po.is_hidden AND g.owner_id=$3)`, id, ownerID, a.ID)
		if e != nil {
			return nil, e
		}
		if !yes {
			return nil, err
		}
	}
	return object(ctx, s.DB, `SELECT `+postProjection(ctx)+postJoin+` WHERE po.id=$1 AND NOT po.is_hidden`, id, a.Cohort)
}

type PostInput struct {
	OnBehalfOf    string `json:"on_behalf_of"`
	Body          string `json:"body"`
	ReplyTo       *int64 `json:"reply_to"`
	Ping          bool   `json:"ping"`
	ShareActivity *int64 `json:"share_activity"`
	SharePlace    *int64 `json:"share_place"`
	ShareEvent    *int64 `json:"share_event"`
}

func (p *PostInput) validate() error { return p.validateAttachment(false) }
func (p *PostInput) validateAttachment(hasAttachment bool) error {
	p.Body = strings.TrimSpace(p.Body)
	n := 0
	for _, id := range []*int64{p.ShareActivity, p.SharePlace, p.ShareEvent} {
		if id != nil {
			if *id <= 0 {
				return platform.ErrInvalid
			}
			n++
		}
	}
	if n > 1 || utf8.RuneCountInString(p.Body) > 4000 || p.Body == "" && n == 0 && !hasAttachment {
		return platform.ErrInvalid
	}
	if p.ReplyTo != nil && *p.ReplyTo <= 0 {
		return platform.ErrInvalid
	}
	return nil
}
func validateShare(ctx context.Context, q platform.Querier, a Actor, p PostInput) error {
	if p.ShareActivity != nil {
		v, err := activity(ctx, q, a, *p.ShareActivity, false)
		if err != nil {
			return err
		}
		if v.Status == "cancelled" {
			return platform.ErrInvalid
		}
	}
	if p.SharePlace != nil {
		if err := publicPlace(ctx, q, a, *p.SharePlace, false); err != nil {
			return err
		}
	}
	if p.ShareEvent != nil {
		ok, err := scalar(ctx, q, `SELECT EXISTS(SELECT 1 FROM events_event e WHERE e.id=$1 AND NOT e.is_tombstone AND NOT e.is_import_held AND e.lifecycle_status IN ('scheduled','rescheduled','sold_out') AND (e.place_id IS NULL OR EXISTS(SELECT 1 FROM places_place p WHERE p.id=e.place_id AND `+catalog.PolicyFromContext(ctx).PlaceSQL()+`)))`, *p.ShareEvent)
		if err := errorIfFalse(ok, err); err != nil {
			return err
		}
	}
	return nil
}

type AttachPostFunc func(context.Context, pgx.Tx, int64) error

func (s *Service) WritePost(ctx context.Context, a Actor, kind string, id int64, in PostInput, announcement bool) (int64, error) {
	return s.writePost(ctx, a, kind, id, in, announcement, nil)
}

// WritePostAttached executes the attachment DB publication, post, audit,
// notifications and identifiers-only live event within the same transaction.
func (s *Service) WritePostAttached(ctx context.Context, a Actor, kind string, id int64, in PostInput, announcement bool, attach AttachPostFunc) (int64, error) {
	if attach == nil {
		return 0, platform.ErrInvalid
	}
	return s.writePost(ctx, a, kind, id, in, announcement, attach)
}
func (s *Service) writePost(ctx context.Context, a Actor, kind string, id int64, in PostInput, announcement bool, attach AttachPostFunc) (int64, error) {
	if err := in.validateAttachment(attach != nil); err != nil {
		return 0, err
	}
	if utf8.RuneCountInString(in.Body) > s.Policy.ChatMaxLength {
		return 0, platform.ErrInvalid
	}
	if !s.allow(a.ID, "thread_post", 30, time.Minute) {
		return 0, platform.ErrForbidden
	}
	var postID int64
	err := s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := threadOwner(ctx, tx, a, kind, id, true)
		if announcement && kind == "group" && a.IsStaff {
			g, e := group(ctx, tx, a, id, true, true)
			err = e
			v = threadState{g.ID, g.ThreadID, g.OwnerID, "group", g.Cohort, g.Status, g.Title}
		}
		if err != nil {
			return err
		}
		if announcement {
			if v.Status == "cancelled" || v.Kind == "group" && v.Status != "active" {
				return platform.ErrInvalid
			}
			role, e := memberRole(ctx, tx, a, v)
			if e != nil {
				return e
			}
			if role == "guardian" {
				return platform.ErrForbidden
			}
			if v.Kind == "activity" && a.Cohort != v.Cohort {
				return platform.ErrForbidden
			}
		} else {
			if err := threadGate(ctx, tx, a, v, true); err != nil {
				return err
			}
		}
		if announcement {
			if kind == "activity" {
				av, err := activity(ctx, tx, a, id, false)
				if err != nil {
					return err
				}
				ok, err := organizer(ctx, tx, a, av)
				if err := errorIfFalse(ok, err); err != nil {
					return err
				}
			} else if a.ID != v.OwnerID && !a.IsStaff {
				return platform.ErrForbidden
			}
		}
		if err := validateShare(ctx, tx, a, in); err != nil {
			return err
		}
		if !announcement && s.MessagePolicy != nil {
			processed, e := s.MessagePolicy(ctx, tx, a, v.ThreadID, in.Body)
			if e != nil {
				return e
			}
			if utf8.RuneCountInString(processed) > s.Policy.ChatMaxLength {
				return platform.ErrInvalid
			}
			in.Body = processed
		}
		if in.ReplyTo != nil {
			var parent, tid int64
			var hidden, pinned bool
			if err := tx.QueryRow(ctx, `SELECT COALESCE(reply_to_id,id),thread_id,is_hidden,is_announcement FROM social_post WHERE id=$1`, *in.ReplyTo).Scan(&parent, &tid, &hidden, &pinned); err != nil {
				return err
			}
			if tid != v.ThreadID || hidden || pinned {
				return platform.ErrInvalid
			}
			var available bool
			if err := tx.QueryRow(ctx, `SELECT NOT is_hidden AND NOT is_announcement AND thread_id=$2 FROM social_post WHERE id=$1`, parent, v.ThreadID).Scan(&available); err != nil {
				return err
			}
			if !available {
				return platform.ErrInvalid
			}
			in.ReplyTo = &parent
		}
		err = tx.QueryRow(ctx, `INSERT INTO social_post(thread_id,author_id,body,is_announcement,is_hidden,is_author_deleted,reply_to_id,shared_activity_id,shared_place_id,shared_event_id,created_at,updated_at) VALUES($1,$2,$3,$4,false,false,$5,$6,$7,$8,now(),now()) RETURNING id`, v.ThreadID, a.ID, in.Body, announcement, in.ReplyTo, in.ShareActivity, in.SharePlace, in.ShareEvent).Scan(&postID)
		if err != nil {
			return err
		}
		if attach != nil {
			if err := attach(ctx, tx, postID); err != nil {
				return err
			}
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_attachment WHERE post_id=$1 AND uploader_id=$2 AND purged_at IS NULL AND ((status='ready' AND storage_key<>'') OR (kind='video' AND status IN('pending','processing') AND source_storage_key<>'')))`, postID, a.ID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return platform.ErrInvalid
			}
		}
		if announcement {
			if err := s.announce(ctx, tx, a, v, in.Body); err != nil {
				return err
			}
		} else if in.Ping {
			if err := s.mentions(ctx, tx, a, v, in.Body); err != nil {
				return err
			}
		}
		if err := chat.Publish(ctx, tx, chat.Event{Kind: "chat", Event: "message", RoomID: v.ThreadID, MessageID: postID}); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "post.created", "post", postID, nil)
	})
	if err != nil {
		return 0, err
	}
	return postID, nil
}
func (s *Service) announce(ctx context.Context, tx pgx.Tx, a Actor, v threadState, body string) error {
	if v.Kind == "activity" {
		return s.fanout(ctx, tx, a, activityState{ID: v.ID, Title: v.Title}, "announcement", "Announcement: "+v.Title, preview(body, 140))
	}
	ids, err := groupRecipients(ctx, tx, a, v.ID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.notify(ctx, tx, id, "group_announcement", "Group announcement: "+v.Title, preview(body, 140), fmt.Sprintf("/groups/%d/", v.ID)); err != nil {
			return err
		}
	}
	return nil
}
func preview(v string, n int) string {
	v = strings.TrimSpace(v)
	r := []rune(v)
	if len(r) > n {
		return strings.TrimSpace(string(r[:n-1])) + "…"
	}
	return v
}

var mentionPattern = regexp.MustCompile(`(?:^|[^\p{L}\p{N}_@])@([\p{L}\p{N}_.-]{1,150})`)

func (s *Service) mentions(ctx context.Context, tx pgx.Tx, a Actor, v threadState, body string) error {
	seen := map[string]bool{}
	names := []string{}
	for _, m := range mentionPattern.FindAllStringSubmatch(body, -1) {
		name := strings.ToLower(m[1])
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	table, key := "social_membership", "activity_id"
	if v.Kind == "group" {
		table, key = "social_groupmembership", "group_id"
	}
	rows, err := tx.Query(ctx, `SELECT u.id FROM `+table+` m JOIN accounts_user u ON u.id=m.user_id WHERE m.`+key+`=$1 AND m.state='member' AND m.role<>'guardian' AND u.id<>$2 AND u.cohort=$3 AND u.is_active AND lower(u.username)=ANY($4) AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$2 AND b.blocked_id=u.id) OR (b.blocker_id=u.id AND b.blocked_id=$2))`, v.ID, a.ID, v.Cohort, names)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	url := fmt.Sprintf("/activities/%d/", v.ID)
	if v.Kind == "group" {
		url = fmt.Sprintf("/groups/%d/", v.ID)
	}
	for _, id := range ids {
		if err := s.notify(ctx, tx, id, "mention", "Someone mentioned you", v.Title, url); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) postOwner(ctx context.Context, tx pgx.Tx, a Actor, id int64) (threadState, error) {
	var aid, gid int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(th.activity_id,0),COALESCE(th.group_id,0) FROM social_post p JOIN social_thread th ON th.id=p.thread_id WHERE p.id=$1`, id).Scan(&aid, &gid); err != nil {
		return threadState{}, err
	}
	kind, ownerID := "activity", aid
	if gid > 0 {
		kind, ownerID = "group", gid
	}
	return threadOwner(ctx, tx, a, kind, ownerID, true)
}
func (s *Service) EditPost(ctx context.Context, a Actor, id int64, body string) error {
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) > s.Policy.ChatMaxLength {
		return platform.ErrInvalid
	}
	return s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := s.postOwner(ctx, tx, a, id)
		if err != nil {
			return err
		}
		if err := threadGate(ctx, tx, a, v, true); err != nil {
			return err
		}
		var authorID int64
		var hidden, pinned bool
		if err = tx.QueryRow(ctx, `SELECT author_id,is_hidden,is_announcement FROM social_post WHERE id=$1 FOR UPDATE`, id).Scan(&authorID, &hidden, &pinned); err != nil {
			return err
		}
		if authorID != a.ID {
			return platform.ErrForbidden
		}
		if hidden || pinned {
			return platform.ErrInvalid
		}
		if s.MessagePolicy != nil {
			processed, e := s.MessagePolicy(ctx, tx, a, v.ThreadID, body)
			if e != nil {
				return e
			}
			if strings.TrimSpace(processed) == "" || utf8.RuneCountInString(processed) > s.Policy.ChatMaxLength {
				return platform.ErrInvalid
			}
			body = processed
		}
		if _, err = tx.Exec(ctx, `UPDATE social_post SET body=$2,updated_at=now() WHERE id=$1`, id, body); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE social_postconcernstate SET note_barred=true WHERE post_id=$1 AND note_sent_at IS NOT NULL`, id); err != nil {
			return err
		}
		if err := chat.Publish(ctx, tx, chat.Event{Kind: "chat", Event: "message", RoomID: v.ThreadID, MessageID: id}); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "post.edited", "post", id, nil)
	})
}
func (s *Service) DeletePost(ctx context.Context, a Actor, id int64) (bool, error) {
	var moderationHidden bool
	err := s.privacyTransaction(ctx, a, func(tx pgx.Tx) error {
		var authorID int64
		var hidden, deleted bool
		if err := tx.QueryRow(ctx, `SELECT author_id,is_hidden,is_author_deleted FROM social_post WHERE id=$1 FOR UPDATE`, id).Scan(&authorID, &hidden, &deleted); err != nil {
			return err
		}
		if authorID != a.ID {
			return platform.ErrForbidden
		}
		if deleted {
			return nil
		}
		var pending bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM safety_moderationaction ma JOIN django_content_type ct ON ct.id=ma.target_type_id WHERE ct.app_label='social' AND ct.model='post' AND ma.target_id=$1 AND ma.action='remove' AND ma.lifted_at IS NULL), EXISTS(SELECT 1 FROM safety_moderationappeal ap JOIN safety_moderationaction ma ON ma.id=ap.action_id JOIN django_content_type ct ON ct.id=ma.target_type_id WHERE ct.app_label='social' AND ct.model='post' AND ma.target_id=$1 AND ma.action='remove' AND ma.lifted_at IS NULL AND ap.status='pending')`, id).Scan(&moderationHidden, &pending)
		if err != nil {
			return err
		}
		if hidden && moderationHidden && pending {
			return platform.ErrForbidden
		}
		if hidden {
			_, err = tx.Exec(ctx, `UPDATE social_post SET is_author_deleted=true WHERE id=$1`, id)
		} else {
			_, err = tx.Exec(ctx, `UPDATE social_post SET is_hidden=true,is_author_deleted=true,updated_at=now() WHERE id=$1`, id)
		}
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "post.self_deleted", "post", id, nil)
	})
	return moderationHidden, err
}

var Facets = []string{"helped_me", "felt_welcome", "made_me_smile", "want_to_come", "got_me_thinking"}

func validFacet(value string) bool {
	for _, v := range Facets {
		if value == v {
			return true
		}
	}
	return false
}
func (s *Service) ToggleSentiment(ctx context.Context, a Actor, id int64, kind, facet string) (bool, error) {
	if kind == "reaction" && !validFacet(facet) {
		return false, platform.ErrInvalid
	}
	if kind != "reaction" && kind != "dissent" && kind != "concern" {
		return false, platform.ErrInvalid
	}
	if !s.allow(a.ID, "thread_react", 60, time.Minute) {
		return false, platform.ErrForbidden
	}
	var added bool
	err := s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := s.postOwner(ctx, tx, a, id)
		if err != nil {
			return err
		}
		if err := sentimentGate(ctx, tx, a, v); err != nil {
			return err
		}
		var hidden bool
		if err := tx.QueryRow(ctx, `SELECT is_hidden FROM social_post WHERE id=$1 FOR UPDATE`, id).Scan(&hidden); err != nil {
			return err
		}
		if hidden {
			return platform.ErrInvalid
		}
		if kind != "reaction" && (a.Cohort == "child" || v.Cohort == "child") {
			return platform.ErrForbidden
		}
		table := "social_post" + kind
		args := []any{id, a.ID}
		extra := ""
		if kind == "reaction" {
			args = append(args, facet)
			extra = ` AND emoji=$3`
		}
		tag, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE post_id=$1 AND user_id=$2`+extra, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			sql := `INSERT INTO ` + table + `(post_id,user_id,created_at) VALUES($1,$2,now()) ON CONFLICT DO NOTHING`
			if kind == "reaction" {
				sql = `INSERT INTO social_postreaction(post_id,user_id,emoji,created_at) VALUES($1,$2,$3,now()) ON CONFLICT DO NOTHING`
			}
			tag, err = tx.Exec(ctx, sql, args...)
			if err != nil {
				return err
			}
			added = tag.RowsAffected() > 0
		}
		event := "post." + kind + "_toggled"
		return s.audit(ctx, tx, a, event, "post", id, map[string]any{"facet": facet, "added": added})
	})
	return added, err
}
