package safety

import (
	"context"
	"encoding/json"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

var activityLabels = map[string]string{"activity.arrived": "You marked yourself as arrived at an activity", "activity.cancelled": "You cancelled an activity you organised", "connection.requested": "You sent a connection request", "connection.accepted": "You accepted a connection", "connection.removed": "You removed a connection", "group.created": "You created a group", "group.joined": "You joined a group", "group.left": "You left a group", "group.archived": "You archived a group", "guardian.link_invited": "You sent a guardian-link invitation", "guardian.link_accepted": "You accepted a guardian link", "media.uploaded": "You uploaded a photo", "media.deleted": "You deleted a photo", "media.attachment_uploaded": "You uploaded a file to a conversation", "media.attachment_deleted": "You deleted an attachment", "messaging.direct_started": "You started a conversation", "messaging.group_started": "You started a group conversation", "messaging.invite_accepted": "You joined a conversation", "messaging.invite_declined": "You declined a conversation invitation", "messaging.key_registered": "You set up message encryption", "messaging.key_verified": "You verified a contact's safety number", "messaging.left": "You left a conversation", "notification.preferences_updated": "You updated your notification settings", "post.self_deleted": "You deleted one of your posts", "user.blocked": "You blocked someone", "user.unblocked": "You unblocked someone"}

func (s *Service) ActivityLog(ctx context.Context, user int64, limits ...int) ([]map[string]any, error) {
	limit := 100
	if len(limits) > 1 {
		return nil, platform.ErrInvalid
	}
	if len(limits) == 1 {
		limit = limits[0]
	}
	if limit < 0 || limit > 1000 {
		return nil, platform.ErrInvalid
	}
	events := []string{}
	for key := range activityLabels {
		events = append(events, key)
	}
	rows, err := objects(ctx, s.DB, `SELECT jsonb_build_object('event',event,'when',created_at) FROM safety_auditlog WHERE actor_ref=$1 AND event=ANY($2) ORDER BY id DESC LIMIT $3`, user, events, limit)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, raw := range rows {
		var row map[string]any
		if err = json.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		row["label"] = activityLabels[row["event"].(string)]
		delete(row, "event")
		out = append(out, row)
	}
	return out, nil
}
