package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestNativeGroupDetailRetainedStaffMembershipKeepsAskUIButServiceStillDenies(t *testing.T) {
	f := newGroupNativeAppFixture(t)
	ctx := context.Background()
	curator, viewer := f.owner, f.peer
	curator.IsStaff, viewer.IsStaff = true, true
	if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET is_staff=true WHERE id=$1 OR id=$2`, curator.ID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	f.app.Social.MinorOnboardingEnabled = true // synthetic fixture only
	group, err := f.app.Social.CreateGroup(ctx, curator, social.GroupInput{City: "Synthetic Retained Staff City", ActivityType: &f.typeID, Title: "Generated retained staff group", Cohort: "child"})
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately model an existing/retained raw row; do not use or loosen JoinGroup.
	if _, err := f.app.DB.Exec(ctx, `INSERT INTO social_groupmembership(group_id,user_id,role,state,joined_at) VALUES($1,$2,'member','member',now())`, group, viewer.ID); err != nil {
		t.Fatal(err)
	}
	child := testdb.Actor(t, f.app.DB, "generated-retained-staff-private-child", "child")
	if _, err := f.app.DB.Exec(ctx, `INSERT INTO social_post(thread_id,author_id,body,is_announcement,is_hidden,is_author_deleted,created_at,updated_at) SELECT id,$2,'PRIVATE-retained-minor-peer',false,false,false,now(),now() FROM social_thread WHERE group_id=$1`, group, child.ID); err != nil {
		t.Fatal(err)
	}
	credentials := groupDetailAppLogin(t, f, viewer)
	path := fmt.Sprintf("/groups/%d/", group)
	body := groupDetailAppHTML(t, familyAppRequest(t, f.app, credentials, "GET", path, "", nil, false))
	for _, want := range []string{"Generated retained staff group", `action="` + path + `ask/"`, `name="prompt"`, `value="next_meetup"`, `value="more_info"`} {
		if !strings.Contains(body, want) {
			t.Fatal("registered staff metadata GET lost existing question affordance", want)
		}
	}
	for _, forbidden := range []string{child.Username, "PRIVATE-retained-minor-peer", "Members (", `id="compose"`, `thread-chat-config`, `/static/js/thread-chat.js`, `action="` + path + `archive/"`} {
		if strings.Contains(body, forbidden) {
			t.Fatal("staff metadata exposed peer/private/owner content", forbidden)
		}
	}
	before := groupDetailAppState(t, f, group)
	w := familyAppRequest(t, f.app, credentials, "POST", path+"ask/", "https://app.example", url.Values{"prompt": {"next_meetup"}}, true)
	var notices, audits int
	if err := f.app.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM notifications_notification WHERE kind='group_question'),(SELECT count(*) FROM safety_auditlog WHERE event='group.question_asked' AND target_ref=$1)`, fmt.Sprintf("social.group:%d", group)).Scan(&notices, &audits); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusNotFound || notices != 0 || audits != 0 || groupDetailAppState(t, f, group) != before {
		t.Fatal("question UI was promoted to cross-cohort service authority", w.Code, notices, audits)
	}
}
