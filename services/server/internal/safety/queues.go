package safety

import (
	"context"
	"encoding/json"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"sort"
	"time"
)

func (s *Service) Report(ctx context.Context, id int64, staff bool) (json.RawMessage, error) {
	if !staff {
		return object(ctx, s.DB, `SELECT jsonb_build_object('id',id,'reason',reason,'detail',detail,'status',status,'created_at',created_at) FROM safety_report WHERE id=$1`, id)
	}
	reports, err := s.reportRows(ctx, "", id)
	if err != nil {
		return nil, err
	}
	if len(reports) == 0 {
		return nil, platform.ErrNotFound
	}
	reports[0].Object["triage"] = nil
	raw, err := json.Marshal(reports[0].Object)
	return raw, err
}

type reportRow struct {
	Object               map[string]any
	Body                 string
	Severity, Duplicates int
	Child                bool
	Created              time.Time
}

func (s *Service) reportRows(ctx context.Context, status string, id int64) ([]reportRow, error) {
	rows, err := s.DB.Query(ctx, `SELECT jsonb_build_object('id',r.id,'reason',r.reason,'detail',r.detail,'status',r.status,'target_type',r.target_type_id,'target_id',r.target_id,'target',CASE c.app_label||'.'||c.model WHEN 'accounts.user' THEN COALESCE(NULLIF(u.display_name,''),u.username) WHEN 'social.activity' THEN a.title WHEN 'social.post' THEN 'post('||COALESCE(NULLIF(owner.display_name,''),owner.username)||' @ '||p.thread_id::text||')' ELSE NULL END,'reporter',r.reporter_id,'handled_by',r.handled_by_id,'handled_at',r.handled_at,'resolution',r.resolution,'created_at',r.created_at),COALESCE(p.body,''),COALESCE(owner.cohort,u.cohort)='child',(SELECT count(*) FROM safety_report d WHERE d.target_type_id=r.target_type_id AND d.target_id=r.target_id AND d.status='open'),CASE r.reason WHEN 'csam' THEN 5 WHEN 'grooming' THEN 5 WHEN 'off_platform' THEN 3 WHEN 'harassment' THEN 2 WHEN 'spam' THEN 1 ELSE 0 END,r.created_at FROM safety_report r JOIN django_content_type c ON c.id=r.target_type_id LEFT JOIN accounts_user u ON c.app_label='accounts' AND c.model='user' AND u.id=r.target_id LEFT JOIN social_activity a ON c.app_label='social' AND c.model='activity' AND a.id=r.target_id LEFT JOIN social_post p ON c.app_label='social' AND c.model='post' AND p.id=r.target_id LEFT JOIN accounts_user owner ON owner.id=COALESCE(a.owner_id,p.author_id) WHERE ($1='' OR r.status=$1) AND ($2::bigint=0 OR r.id=$2) ORDER BY CASE r.reason WHEN 'csam' THEN 5 WHEN 'grooming' THEN 5 WHEN 'off_platform' THEN 3 WHEN 'harassment' THEN 2 WHEN 'spam' THEN 1 ELSE 0 END DESC,r.created_at DESC,r.id DESC LIMIT 100`, status, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []reportRow{}
	for rows.Next() {
		var raw []byte
		var row reportRow
		var child *bool
		if err = rows.Scan(&raw, &row.Body, &child, &row.Duplicates, &row.Severity, &row.Created); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &row.Object) != nil {
			return nil, platform.ErrInvalid
		}
		row.Child = child != nil && *child
		out = append(out, row)
	}
	return out, rows.Err()
}
func (s *Service) ReportQueue(ctx context.Context, a platform.Actor, status string) ([]map[string]any, error) {
	if !a.Moderator() {
		return nil, platform.ErrForbidden
	}
	rows, err := s.reportRows(ctx, status, 0)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		terms := ContactHintTerms(rows[i].Body)
		rows[i].Object["triage"] = map[string]any{"severity": rows[i].Severity, "involves_child": rows[i].Child, "open_duplicates": rows[i].Duplicates, "contact_hint": len(terms) > 0, "contact_terms": terms}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Severity != b.Severity {
			return a.Severity > b.Severity
		}
		if a.Child != b.Child {
			return a.Child
		}
		if a.Duplicates != b.Duplicates {
			return a.Duplicates > b.Duplicates
		}
		ah, bh := a.Object["triage"].(map[string]any)["contact_hint"].(bool), b.Object["triage"].(map[string]any)["contact_hint"].(bool)
		if ah != bh {
			return ah
		}
		return a.Created.After(b.Created)
	})
	out := []map[string]any{}
	for _, row := range rows {
		out = append(out, row.Object)
	}
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		filter := status
		if filter == "" {
			filter = "all"
		}
		return platform.RecordAudit(ctx, tx, a, "moderation.queue_viewed", "", map[string]any{"status": filter, "count": len(out)})
	})
	return out, err
}

func (s *Service) Appeals(ctx context.Context, user int64) ([]json.RawMessage, error) {
	return objects(ctx, s.DB, `SELECT jsonb_build_object('action_label',CASE m.action WHEN 'warn' THEN 'Warn' WHEN 'remove' THEN 'Remove content' WHEN 'suspend' THEN 'Suspend account' WHEN 'timed_ban' THEN 'Timed ban' WHEN 'ban' THEN 'Ban account' ELSE m.action END,'reason_label',CASE m.reason WHEN 'grooming' THEN 'Grooming / predatory contact' WHEN 'harassment' THEN 'Harassment / bullying' WHEN 'csam' THEN 'Child sexual abuse material' WHEN 'spam' THEN 'Spam' WHEN 'off_platform' THEN 'Unsafe off-platform / meetup risk' WHEN 'other' THEN 'Other' ELSE m.reason END,'status',ap.status,'status_label',CASE ap.status WHEN 'pending' THEN 'Pending review' WHEN 'upheld' THEN 'Upheld (decision stands)' WHEN 'overturned' THEN 'Overturned (decision reversed)' END,'statement',ap.statement,'created_at',ap.created_at,'decided_at',ap.decided_at) FROM safety_moderationappeal ap JOIN safety_moderationaction m ON m.id=ap.action_id WHERE ap.appellant_id=$1 ORDER BY ap.created_at DESC,ap.id DESC LIMIT 50`, user)
}

const appealProjection = `jsonb_build_object('id',ap.id,'action',ap.action_id,'appellant',ap.appellant_id,'statement',ap.statement,'status',ap.status,'decided_by',ap.decided_by_id,'decision_notes',ap.decision_notes,'decided_at',ap.decided_at,'created_at',ap.created_at,'content_author_deleted',m.action='remove' AND c.app_label='social' AND c.model='post' AND COALESCE(p.is_hidden AND p.is_author_deleted,false))`

func (s *Service) Appeal(ctx context.Context, id int64) (json.RawMessage, error) {
	return object(ctx, s.DB, `SELECT `+appealProjection+` FROM safety_moderationappeal ap JOIN safety_moderationaction m ON m.id=ap.action_id JOIN django_content_type c ON c.id=m.target_type_id LEFT JOIN social_post p ON c.app_label='social' AND c.model='post' AND p.id=m.target_id WHERE ap.id=$1`, id)
}
func (s *Service) AppealQueue(ctx context.Context, a platform.Actor, status string) ([]json.RawMessage, error) {
	if !a.Moderator() {
		return nil, platform.ErrForbidden
	}
	return objects(ctx, s.DB, `SELECT `+appealProjection+` FROM safety_moderationappeal ap JOIN safety_moderationaction m ON m.id=ap.action_id JOIN django_content_type c ON c.id=m.target_type_id LEFT JOIN social_post p ON c.app_label='social' AND c.model='post' AND p.id=m.target_id WHERE ($1='' OR ap.status=$1) ORDER BY ap.created_at,ap.id LIMIT 200`, status)
}
