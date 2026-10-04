package accounts

import (
	"context"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"net/http"
)

const reasonLabel = `CASE reason WHEN 'grooming' THEN 'Grooming / predatory contact' WHEN 'harassment' THEN 'Harassment / bullying' WHEN 'csam' THEN 'Child sexual abuse material' WHEN 'spam' THEN 'Spam' WHEN 'off_platform' THEN 'Unsafe off-platform / meetup risk' WHEN 'other' THEN 'Other' ELSE reason END`

// SafetyRecord is strictly scoped to one authenticated data subject. It excludes
// moderator identities, private notes, targets reported and other members.
func (s *Service) SafetyRecord(ctx context.Context, user int64) (map[string]any, error) {
	decisions, err := jsonObjects(ctx, s.DB, `WITH recent AS (
(SELECT m.*, 'your account'::text AS scope FROM safety_moderationaction m JOIN django_content_type c ON c.id=m.target_type_id WHERE c.app_label='accounts' AND c.model='user' AND m.target_id=$1 ORDER BY m.created_at DESC,m.id DESC LIMIT 50)
UNION ALL (SELECT m.*, 'one of your activities'::text AS scope FROM safety_moderationaction m JOIN django_content_type c ON c.id=m.target_type_id JOIN social_activity a ON a.id=m.target_id WHERE c.app_label='social' AND c.model='activity' AND a.owner_id=$1 ORDER BY m.created_at DESC,m.id DESC LIMIT 50)
UNION ALL (SELECT m.*, 'one of your posts'::text AS scope FROM safety_moderationaction m JOIN django_content_type c ON c.id=m.target_type_id JOIN social_post p ON p.id=m.target_id WHERE c.app_label='social' AND c.model='post' AND p.author_id=$1 ORDER BY m.created_at DESC,m.id DESC LIMIT 50)), top AS (SELECT * FROM recent ORDER BY created_at DESC,id DESC LIMIT 50)
SELECT jsonb_build_object('action_id',m.id,'action_label',CASE m.action WHEN 'warn' THEN 'Warn' WHEN 'remove' THEN 'Remove content' WHEN 'suspend' THEN 'Suspend account' WHEN 'timed_ban' THEN 'Timed ban' WHEN 'ban' THEN 'Ban account' ELSE m.action END,'reason_label',`+`CASE m.reason WHEN 'grooming' THEN 'Grooming / predatory contact' WHEN 'harassment' THEN 'Harassment / bullying' WHEN 'csam' THEN 'Child sexual abuse material' WHEN 'spam' THEN 'Spam' WHEN 'off_platform' THEN 'Unsafe off-platform / meetup risk' WHEN 'other' THEN 'Other' ELSE m.reason END`+`,'scope',m.scope,'created_at',m.created_at,'is_sanction',m.action IN ('suspend','timed_ban','ban'),'is_active',m.action IN ('suspend','timed_ban','ban') AND (m.action='ban' OR ((m.expires_at IS NULL OR m.expires_at>$2) AND m.lifted_at IS NULL)),'can_appeal',ap.id IS NULL,'appeal_status_label',CASE ap.status WHEN 'pending' THEN 'Pending review' WHEN 'upheld' THEN 'Upheld (decision stands)' WHEN 'overturned' THEN 'Overturned (decision reversed)' ELSE NULL END,'content_author_deleted',m.scope='one of your posts' AND m.action='remove' AND EXISTS(SELECT 1 FROM social_post p WHERE p.id=m.target_id AND p.is_author_deleted AND p.is_hidden)) FROM top m LEFT JOIN safety_moderationappeal ap ON ap.action_id=m.id ORDER BY m.created_at DESC,m.id DESC`, user, s.Config.Now())
	if err != nil {
		return nil, err
	}
	reports, err := jsonObjects(ctx, s.DB, `SELECT jsonb_build_object('reason_label',`+reasonLabel+`,'status_label',CASE status WHEN 'open' THEN 'Open' WHEN 'reviewing' THEN 'Reviewing' WHEN 'actioned' THEN 'Actioned' WHEN 'dismissed' THEN 'Dismissed' ELSE status END,'created_at',created_at,'handled_at',handled_at,'detail',detail,'resolution',resolution) FROM safety_report WHERE reporter_id=$1 ORDER BY created_at DESC,id DESC LIMIT 50`, user)
	if err != nil {
		return nil, err
	}
	var total, reportsTotal int
	err = s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM safety_moderationaction m JOIN django_content_type c ON c.id=m.target_type_id WHERE c.app_label='accounts' AND c.model='user' AND m.target_id=$1)+(SELECT count(*) FROM safety_moderationaction m JOIN django_content_type c ON c.id=m.target_type_id JOIN social_activity a ON a.id=m.target_id WHERE c.app_label='social' AND c.model='activity' AND a.owner_id=$1)+(SELECT count(*) FROM safety_moderationaction m JOIN django_content_type c ON c.id=m.target_type_id JOIN social_post p ON p.id=m.target_id WHERE c.app_label='social' AND c.model='post' AND p.author_id=$1),(SELECT count(*) FROM safety_report WHERE reporter_id=$1)`, user).Scan(&total, &reportsTotal)
	if err != nil {
		return nil, err
	}
	return map[string]any{"decisions": decisions, "reports": reports, "decisions_total": total, "decisions_truncated": total > len(decisions), "reports_total": reportsTotal, "reports_truncated": reportsTotal > len(reports)}, nil
}

func (s *Service) SafetyRecordHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	payload, err := s.SafetyRecord(r.Context(), a.ID)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, payload)
}
