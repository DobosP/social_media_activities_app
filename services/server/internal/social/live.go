package social

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func (s *Service) ThreadOwner(ctx context.Context, a Actor, threadID int64) (string, int64, error) {
	v, err := threadFromID(ctx, s.DB, a, threadID)
	if err != nil {
		return "", 0, err
	}
	if err := threadGate(ctx, s.DB, a, v, false); err != nil {
		return "", 0, err
	}
	return v.Kind, v.ID, nil
}
func (s *Service) TypingIdentity(ctx context.Context, a Actor, threadID int64) (map[string]any, error) {
	var reloadErr error
	a, reloadErr = actorByID(ctx, s.DB, a.ID)
	if reloadErr != nil {
		_, e := authorizedResult(reloadErr)
		return nil, e
	}
	v, err := threadFromID(ctx, s.DB, a, threadID)
	if err != nil {
		yes, e := authorizedResult(err)
		if !yes && e == nil {
			return nil, nil
		}
		return nil, e
	}
	if err := threadGate(ctx, s.DB, a, v, true); err != nil {
		yes, e := authorizedResult(err)
		if !yes && e == nil {
			return nil, nil
		}
		return nil, e
	}
	name := a.DisplayName
	if name == "" {
		name = a.Username
	}
	return map[string]any{"author_id": a.ID, "author": name}, nil
}
func (s *Service) LivePost(ctx context.Context, a Actor, id int64) (map[string]any, error) {
	var tid int64
	if err := s.DB.QueryRow(ctx, `SELECT thread_id FROM social_post WHERE id=$1 AND NOT is_hidden`, id).Scan(&tid); err != nil {
		return nil, err
	}
	v, err := threadFromID(ctx, s.DB, a, tid)
	if err != nil {
		return nil, err
	}
	if err := threadGate(ctx, s.DB, a, v, false); err != nil {
		return nil, err
	}
	raw, err := s.Post(ctx, a, id)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, platform.ErrInvalid
	}
	var authorID int64
	var username, display string
	var created, updated time.Time
	var parentID *int64
	if err := s.DB.QueryRow(ctx, `SELECT p.author_id,u.username,u.display_name,p.created_at,p.updated_at,p.reply_to_id FROM social_post p JOIN accounts_user u ON u.id=p.author_id WHERE p.id=$1`, id).Scan(&authorID, &username, &display, &created, &updated, &parentID); err != nil {
		return nil, err
	}
	author := display
	if author == "" {
		author = username
	}
	out["author"] = author
	out["author_id"] = authorID
	out["edited"] = updated.After(created)
	out["created_at"] = created.UTC().Format(time.RFC3339Nano)
	out["reply_snippet"] = nil
	if parentID != nil {
		var parentBody, parentDisplay, parentName string
		var hidden bool
		if err := s.DB.QueryRow(ctx, `SELECT p.body,p.is_hidden,u.display_name,u.username FROM social_post p JOIN accounts_user u ON u.id=p.author_id WHERE p.id=$1`, *parentID).Scan(&parentBody, &hidden, &parentDisplay, &parentName); err != nil {
			return nil, err
		}
		label := parentDisplay
		if label == "" {
			label = parentName
		}
		text := strings.ReplaceAll(strings.TrimSpace(parentBody), "\n", " ")
		if hidden {
			text = "(message removed)"
		} else {
			text = preview(text, 120)
		}
		out["reply_snippet"] = map[string]any{"author": label, "text": text, "pk": *parentID}
	}
	roster := map[string]bool{}
	if v.Kind != "group" {
		rows, err := s.DB.Query(ctx, `SELECT u.username FROM social_membership m JOIN accounts_user u ON u.id=m.user_id WHERE m.activity_id=$1 AND m.state='member' AND m.role<>'guardian' AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$2 AND b.blocked_id=u.id) OR (b.blocker_id=u.id AND b.blocked_id=$2))`, v.ID, a.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return nil, err
			}
			roster[strings.ToLower(name)] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if s.BodyMarkup == nil {
		return nil, fmt.Errorf("shared body markup adapter unavailable")
	}
	body, _ := out["body"].(string)
	out["body_html"] = s.BodyMarkup(body, roster, v.Cohort == "adult")
	rows, err := s.DB.Query(ctx, `SELECT id FROM media_attachment WHERE post_id=$1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	attachments := []int64{}
	for rows.Next() {
		var aid int64
		if err := rows.Scan(&aid); err != nil {
			rows.Close()
			return nil, err
		}
		attachments = append(attachments, aid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out["attachment_ids"] = attachments
	return out, nil
}
