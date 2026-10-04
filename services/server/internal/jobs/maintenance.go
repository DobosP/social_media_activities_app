package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (r *Runner) purgeMessages(ctx context.Context) (int64, error) {
	var count int64
	err := platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT m.id FROM messaging_message m JOIN messaging_conversation c ON c.id=m.conversation_id WHERE (c.disappearing_seconds>0 AND m.created_at<$1::timestamptz-make_interval(secs=>c.disappearing_seconds)) OR ($2::integer>0 AND m.created_at<$1::timestamptz-make_interval(days=>$2)) FOR UPDATE OF m`, r.Config.Now(), r.Config.MessagingRetentionDays)
		if err != nil {
			return err
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
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
		if len(ids) == 0 {
			return nil
		}
		if _, err = tx.Exec(ctx, `DELETE FROM messaging_messagekey WHERE message_id=ANY($1)`, ids); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM messaging_message WHERE id=ANY($1)`, ids)
		count = tag.RowsAffected()
		return err
	})
	return count, err
}
func (r *Runner) purgeNotifications(ctx context.Context, tx pgx.Tx, days, batch int) (int64, error) {
	if days <= 0 || batch <= 0 {
		return 0, nil
	}
	tag, err := tx.Exec(ctx, `DELETE FROM notifications_notification WHERE id IN (SELECT id FROM notifications_notification WHERE read_at IS NOT NULL AND created_at<$1 AND kind NOT IN ('moderation','system') ORDER BY id LIMIT $2 FOR UPDATE SKIP LOCKED)`, r.Config.Now().Add(-time.Duration(days)*24*time.Hour), min(batch, r.Config.NotificationBatch))
	return tag.RowsAffected(), err
}
func jsonInt(payload map[string]json.RawMessage, key string, fallback int) int {
	var value int
	if raw, ok := payload[key]; ok && json.Unmarshal(raw, &value) == nil {
		return value
	}
	return fallback
}
func jsonString(payload map[string]json.RawMessage, key, defaultValue string) string {
	var value string
	if raw, ok := payload[key]; ok && json.Unmarshal(raw, &value) == nil {
		return value
	}
	return defaultValue
}
func clampText(value string, limit int) string {
	chars := []rune(value)
	if len(chars) > limit {
		return string(chars[:limit])
	}
	return value
}

func (r *Runner) installDeferred() {
	_ = r.Queue.Register("erasure.blob_cleanup", func(ctx context.Context, tx pgx.Tx, payload map[string]json.RawMessage) error {
		var keys []string
		if raw, ok := payload["blob_keys"]; ok {
			if json.Unmarshal(raw, &keys) != nil {
				var one string
				if json.Unmarshal(raw, &one) != nil {
					return errors.New("blob keys must be strings")
				}
				keys = []string{one}
			}
		}
		if len(keys) > r.Config.BlobBatch {
			// Queue the complete remainder in the same transaction. Completing
			// this bounded chunk must never silently forget physical erasure.
			remaining := append([]string(nil), keys[r.Config.BlobBatch:]...)
			encoded, _ := json.Marshal(remaining)
			digest := sha256.Sum256(encoded)
			if _, err := r.Queue.Enqueue(ctx, tx, "erasure.blob_cleanup", map[string]any{"blob_keys": remaining}, ops.EnqueueOptions{DedupKey: "blob-continuation:" + hex.EncodeToString(digest[:])}); err != nil {
				return err
			}
			keys = keys[:r.Config.BlobBatch]
		}
		if len(keys) == 0 {
			return nil
		}
		if r.Config.DeleteBlob == nil {
			return missing()
		}
		for _, key := range keys {
			if strings.TrimSpace(key) == "" {
				continue
			}
			if len(key) > 160 || strings.ContainsAny(key, "\\\x00") || strings.Contains(key, "..") {
				return errors.New("invalid private object reference")
			}
			if err := r.Config.DeleteBlob(ctx, key); err != nil {
				return err
			}
		}
		return platform.RecordAudit(ctx, tx, platform.Actor{}, "erasure.blob_cleanup", "", map[string]int{"blob_count": len(keys)})
	})
	_ = r.Queue.Register("media.scan.dispatch", func(ctx context.Context, tx pgx.Tx, payload map[string]json.RawMessage) error {
		return platform.RecordAudit(ctx, tx, platform.Actor{}, "media.scan_dispatch_blocked", "", map[string]any{"attachment_id": jsonInt(payload, "attachment_id", 0), "photo_id": jsonInt(payload, "photo_id", 0), "reason": "withheld_state_not_implemented"})
	})
	_ = r.Queue.Register("notifications.retention_purge", func(ctx context.Context, tx pgx.Tx, payload map[string]json.RawMessage) error {
		_, err := r.purgeNotifications(ctx, tx, jsonInt(payload, "days", r.Config.NotificationRetentionDays), jsonInt(payload, "batch_size", r.Config.NotificationBatch))
		return err
	})
	_ = r.Queue.Register("notify.activity_fanout", func(ctx context.Context, tx pgx.Tx, payload map[string]json.RawMessage) error {
		activity := jsonInt(payload, "activity_id", 0)
		if activity < 1 {
			return errors.New("activity required")
		}
		kind := jsonString(payload, "kind", "announcement")
		if !notificationKinds[kind] {
			return errors.New("invalid notification kind")
		}
		title := clampText(jsonString(payload, "title", ""), 200)
		body := clampText(jsonString(payload, "body", ""), 600)
		url := clampText(jsonString(payload, "url", fmt.Sprintf("/api/social/activities/%d/", activity)), 300)
		exclude := jsonInt(payload, "exclude_user_id", 0)
		rows, err := tx.Query(ctx, `SELECT m.user_id FROM social_membership m JOIN social_activity a ON a.id=m.activity_id WHERE a.id=$1 AND NOT a.is_hidden AND m.state='member' AND m.user_id<>$2 AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=a.owner_id AND b.blocked_id=m.user_id) OR (b.blocker_id=m.user_id AND b.blocked_id=a.owner_id)) AND NOT EXISTS(SELECT 1 FROM notifications_notification n WHERE n.recipient_id=m.user_id AND n.kind=$3 AND n.title=$4 AND n.url=$5) ORDER BY m.id LIMIT 500`, activity, exclude, kind, title, url)
		if err != nil {
			return err
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
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
		for _, id := range ids {
			if _, err = platform.Notify(ctx, tx, id, kind, title, body, url); err != nil {
				return err
			}
		}
		return nil
	})
	_ = r.Queue.Register("cron.run_command", func(ctx context.Context, tx pgx.Tx, payload map[string]json.RawMessage) error {
		command := jsonString(payload, "command", "")
		allowed := false
		for _, name := range DueNames {
			if command == name && name != "process_deferred_tasks" {
				allowed = true
			}
		}
		if !allowed {
			return errors.New("nonallowlisted periodic job")
		}
		var options map[string]json.RawMessage
		if raw, ok := payload["kwargs"]; ok && json.Unmarshal(raw, &options) != nil {
			return errors.New("invalid job options")
		}
		_, err := r.Run(ctx, command, options)
		return err
	})
}

var notificationKinds = map[string]bool{"join_requested": true, "join_approved": true, "event_reminder": true, "activity_cancelled": true, "activity_updated": true, "announcement": true, "group_announcement": true, "meetup_confirmed": true, "arrival": true, "connection_request": true, "connection_accepted": true, "mention": true, "organizer_role": true, "activity_match": true, "gauge_match": true, "group_question": true, "interest_converted": true, "rsvp_nudge": true, "organizer_prep": true, "supervisor_needed": true, "formative_note": true, "mod_alert": true, "moderation": true, "system": true}

func reminderBody(start time.Time, meeting, bring, first string) string {
	lines := []string{"Starts " + start.Format("2006-01-02 15:04") + "."}
	for _, row := range []struct{ Label, Value string }{{"Meet", meeting}, {"Bring", bring}, {"First time", first}} {
		value := strings.TrimSpace(row.Value)
		if value == "" {
			continue
		}
		if utf8.RuneCountInString(value) > 120 {
			value = strings.TrimSpace(clampText(value, 119)) + "…"
		}
		lines = append(lines, row.Label+": "+value)
	}
	return clampText(strings.Join(lines, "\n"), 600)
}
func (r *Runner) Reminders(ctx context.Context) (int, error) {
	rows, err := r.DB.Query(ctx, `SELECT id FROM social_activity WHERE status='open' AND NOT is_hidden AND starts_at>=$1 AND starts_at<=$2 ORDER BY id`, r.Config.Now(), r.Config.Now().Add(time.Duration(r.Config.ReminderHours)*time.Hour))
	if err != nil {
		return 0, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, id := range ids {
		err = platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error {
			var title, meeting, bring, first string
			var starts time.Time
			if err := tx.QueryRow(ctx, `SELECT title,starts_at,meeting_point,what_to_bring,first_time_note FROM social_activity WHERE id=$1 AND NOT is_hidden AND status='open' FOR UPDATE`, id).Scan(&title, &starts, &meeting, &bring, &first); err != nil {
				return err
			}
			url := "/api/social/activities/" + strconv.FormatInt(id, 10) + "/"
			rows, err := tx.Query(ctx, `SELECT user_id FROM social_membership m WHERE activity_id=$1 AND state='member' AND NOT EXISTS(SELECT 1 FROM notifications_notification n WHERE n.recipient_id=m.user_id AND n.kind='event_reminder' AND n.url=$2) ORDER BY m.id`, id, url)
			if err != nil {
				return err
			}
			members := []int64{}
			for rows.Next() {
				var uid int64
				if err = rows.Scan(&uid); err != nil {
					rows.Close()
					return err
				}
				members = append(members, uid)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, uid := range members {
				delivered, err := platform.Notify(ctx, tx, uid, "event_reminder", "“"+title+"” is starting soon", reminderBody(starts, meeting, bring, first), url)
				if err != nil {
					return err
				}
				if delivered {
					sent++
				}
			}
			return nil
		})
		if err != nil {
			return sent, err
		}
	}
	return sent, nil
}
