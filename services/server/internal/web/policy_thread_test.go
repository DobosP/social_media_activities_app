package web

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
)

func TestPostgresBrowserThreadUsesConfiguredRootLimitAndCursor(t *testing.T) {
	s, owner, place, typ := socialLegacyFixture(t)
	ctx := context.Background()
	pk := socialLegacyActivity(t, s, owner, place, typ, "Configured thread page")
	c := social.DefaultPolicyConfig()
	c.ThreadPostLimit = 2
	if err := s.Social.ConfigurePolicy(c); err != nil {
		t.Fatal(err)
	}
	roots := []int64{}
	for _, body := range []string{"Root one", "Root two", "Root three"} {
		id, err := s.Social.WritePost(ctx, owner, "activity", pk, social.PostInput{Body: body}, false)
		if err != nil {
			t.Fatal(err)
		}
		roots = append(roots, id)
	}
	if _, err := s.Social.WritePost(ctx, owner, "activity", pk, social.PostInput{Body: "Visible reply", ReplyTo: &roots[2]}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Social.WritePost(ctx, owner, "activity", pk, social.PostInput{Body: "Separate announcement"}, true); err != nil {
		t.Fatal(err)
	}
	html, data := socialLegacyHTML(t, s, owner, "activity_detail", pk, "")
	if len(spaRows(data["posts"])) != 2 || !spaBool(data["has_older"]) || spaInt(data["older_cursor"]) != int(roots[1]) {
		t.Fatal("configured browser page/cursor ignored", data["posts"], data["older_cursor"])
	}
	if strings.Contains(html, "Root one") || !strings.Contains(html, "Root two") || !strings.Contains(html, "Root three") || !strings.Contains(html, "Visible reply") || !strings.Contains(html, "Separate announcement") {
		t.Fatal("wrong browser root/reply/announcement slice")
	}
	html, data = socialLegacyHTML(t, s, owner, "activity_detail", pk, "before="+fmt.Sprint(roots[1]))
	older := spaRows(data["posts"])
	if len(older) != 1 || spaBool(data["has_older"]) || spaText(older[0]["body"]) != "Root one" || !strings.Contains(html, "Root one") {
		t.Fatal("configured older page ignored", len(older), data["has_older"])
	}
}

func TestPostgresBrowserLargestConfiguredThreadPageChunksModels(t *testing.T) {
	s, owner, place, typ := socialLegacyFixture(t)
	ctx := context.Background()
	pk := socialLegacyActivity(t, s, owner, place, typ, "Large configured thread page")
	c := social.DefaultPolicyConfig()
	c.ThreadPostLimit = 1000
	if err := s.Social.ConfigurePolicy(c); err != nil {
		t.Fatal(err)
	}
	var tid int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM social_thread WHERE activity_id=$1`, pk).Scan(&tid); err != nil {
		t.Fatal(err)
	}
	// Bulk synthetic history avoids turning test setup into an abuse-budget exercise.
	if _, err := s.DB.Exec(ctx, `INSERT INTO social_post(thread_id,author_id,body,is_announcement,is_hidden,is_author_deleted,created_at,updated_at) SELECT $1,$2,'Synthetic historical root '||i,false,false,false,now()+i*interval '1 millisecond',now()+i*interval '1 millisecond' FROM generate_series(1,1000) i`, tid, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Social.WritePost(ctx, owner, "activity", pk, social.PostInput{Body: "Announcement outside root cap"}, true); err != nil {
		t.Fatal(err)
	}
	_, data := socialLegacyHTML(t, s, owner, "activity_detail", pk, "")
	if len(spaRows(data["posts"])) != 1000 || len(spaRows(data["announcements"])) != 1 || spaBool(data["has_older"]) {
		t.Fatal("large configured root/announcement page failed", len(spaRows(data["posts"])), len(spaRows(data["announcements"])))
	}
	posts := spaRows(data["posts"])
	for i, p := range posts {
		if spaText(p["body"]) != fmt.Sprintf("Synthetic historical root %d", i+1) {
			t.Fatal("chunking changed chronological order", i, p["body"])
		}
	}
	// Cross-chunk repeated IDs retain SQL ANY's deduplication behavior.
	duplicateInput := append(append([]map[string]any{}, posts...), posts[0])
	models, err := s.socialPostModels(ctx, owner, tid, duplicateInput)
	if err != nil || len(models) != 1000 {
		t.Fatal("cross-chunk duplicate IDs changed result", len(models), err)
	}
}
