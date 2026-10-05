package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

const webCasePort4FirstNote = "Look for the folks in red bibs by the north gate; we warm up together first."

func TestWebCasePort4FirstTimeCardMembershipScopeAndEscaping(t *testing.T) {
	s, owner, place, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	activity, err := s.Social.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Case4 first time meetup", StartsAt: time.Now().Add(24 * time.Hour), FirstTimeNote: webCasePort4FirstNote})
	if err != nil {
		t.Fatal(err)
	}
	member := webCasePort3Member(t, s, owner, activity, "case-port4-first-note-member")
	stranger := testdb.Actor(t, s.DB, "case-port4-first-note-stranger", "adult")
	path := fmt.Sprintf("/activities/%d/", activity)
	t.Run("member_owner_positive_and_nonmember_negative", func(t *testing.T) {
		for _, viewer := range []platform.Actor{member, owner} {
			webCasePortContains(t, webCasePortHTML(t, mux, viewer, path), "First time here?", webCasePort4FirstNote)
		}
		webCasePortAbsent(t, webCasePortHTML(t, mux, stranger, path), "First time here?", webCasePort4FirstNote)
	})
	t.Run("outside_welcome_window_still_shows_note", func(t *testing.T) {
		for _, value := range []string{"NULL", "now()-interval '8 days'"} {
			if _, err := s.DB.Exec(ctx, `UPDATE social_membership SET welcomed_at=`+value+` WHERE activity_id=$1 AND user_id=$2`, activity, member.ID); err != nil {
				t.Fatal(err)
			}
			webCasePortContains(t, webCasePortHTML(t, mux, member, path), webCasePort4FirstNote)
		}
	})
	t.Run("cohort_read_api_excludes_member_only_and_retired_fields", func(t *testing.T) {
		w := webCasePortRead(mux, stranger, path)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		request := socialLegacyRequest("GET", fmt.Sprintf("/api/social/activities/%d/", activity), owner, 0, nil)
		response := httptest.NewRecorder()
		s.API.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatal("native cohort read", response.Code, response.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"first_time_note", "getting_home_note"} {
			if _, exists := body[field]; exists {
				t.Fatal("cohort DTO exposes source member-only field", field)
			}
		}
	})
	t.Run("owner_edit_path_then_escaped_note_and_empty_card", func(t *testing.T) {
		zone, err := time.LoadLocation("Europe/Bucharest")
		if err != nil {
			t.Fatal(err)
		}
		for _, note := range []string{"brand new arrival note", "<script>alert(1)</script> meet at gate", ""} {
			w := webCasePort2Post(t, mux, owner, path+"edit/", path+"edit/", url.Values{"place": {fmt.Sprint(place)}, "activity_type": {fmt.Sprint(typ)}, "title": {"Case4 first time meetup"}, "description": {""}, "starts_at": {time.Now().Add(24 * time.Hour).In(zone).Format("2006-01-02T15:04")}, "first_time_note": {note}, "cost_band": {"unspecified"}, "difficulty": {"unspecified"}})
			var stored string
			if err := s.DB.QueryRow(ctx, `SELECT first_time_note FROM social_activity WHERE id=$1`, activity).Scan(&stored); err != nil || stored != note || w.Code != 302 {
				t.Fatal("source first-time note owner edit", err, stored, w.Code)
			}
			body := webCasePortHTML(t, mux, member, path)
			switch note {
			case "":
				webCasePortAbsent(t, body, "First time here?")
			case "brand new arrival note":
				webCasePortContains(t, body, note)
			default:
				webCasePortAbsent(t, body, "<script>alert(1)</script>")
				webCasePortContains(t, body, "&lt;script&gt;")
			}
		}
	})
}

func TestWebCasePort4BriefAndAudienceMemberPresentation(t *testing.T) {
	s, owner, place, typ, mux := webCasePortFixture(t)
	activity, err := s.Social.CreateActivity(context.Background(), owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Morning Run", StartsAt: time.Now().Add(2 * time.Hour), CostBand: "free", MeetingPoint: "By the SECRET north gate"})
	if err != nil {
		t.Fatal(err)
	}
	member := webCasePort3Member(t, s, owner, activity, "case-port4-audience-member")
	stranger := testdb.Actor(t, s.DB, "case-port4-audience-stranger", "adult")
	path := fmt.Sprintf("/activities/%d/", activity)
	t.Run("brief_region_gates_logistics", func(t *testing.T) {
		outside := webCasePortHTML(t, mux, stranger, path)
		webCasePortContains(t, outside, `aria-labelledby="brief-heading"`, "At a glance")
		webCasePortAbsent(t, outside, "SECRET north gate")
		body := webCasePortHTML(t, mux, member, path)
		region := regexp.MustCompile(`(?s)aria-labelledby="brief-heading".*?</section>`).FindString(body)
		if region == "" || !strings.Contains(region, "SECRET north gate") {
			t.Fatal("source logistics missing within specifically brief region")
		}
	})
	t.Run("member_audience_negatives_and_exact_adult_peer_line", func(t *testing.T) {
		webCasePortContains(t, webCasePortHTML(t, mux, member, path), "Visible to members of this activity only.", "Never public, never indexed, never shown to other cohorts.", "other person can see this.")
		webCasePortAbsent(t, webCasePortHTML(t, mux, stranger, path), "Never public, never indexed")
	})
}

func TestWebCasePort4UnifiedGroupsPrefillAndCreationPolicy(t *testing.T) {
	s, actor, _, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	curator := testdb.Actor(t, s.DB, "case-port4-discovery-curator", "adult")
	curator.IsStaff = true
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_staff=true WHERE id=$1`, curator.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Social.CreateGroup(ctx, curator, social.GroupInput{City: "Cluj-Napoca", ActivityType: &typ, Title: "W5 Football Crew"}); err != nil {
		t.Fatal(err)
	}
	var typeSlug string
	if err := s.DB.QueryRow(ctx, `SELECT slug FROM taxonomy_activitytype WHERE id=$1`, typ).Scan(&typeSlug); err != nil {
		t.Fatal(err)
	}
	t.Run("groups_redirect_and_one_discovery_surface", func(t *testing.T) {
		w := webCasePortRead(mux, actor, "/groups/")
		if w.Code != 302 || w.Header().Get("Location") != "/communities/" {
			t.Fatal("source groups redirect", w.Code, w.Header().Get("Location"))
		}
		webCasePortContains(t, webCasePortHTML(t, mux, actor, "/communities/"), "W5 Football Crew", "Groups", "Around your city")
	})
	t.Run("curator_prefill_valid_and_unknown_type_dropped", func(t *testing.T) {
		body := webCasePortHTML(t, mux, curator, "/groups/new/?city=Cluj-Napoca&type="+url.QueryEscape(typeSlug))
		webCasePortContains(t, body, `value="Cluj-Napoca"`, fmt.Sprintf(`<option value="%d" selected`, typ))
		body = webCasePortHTML(t, mux, curator, "/groups/new/?type=no-such-type")
		selectHTML := regexp.MustCompile(`(?s)<select[^>]*name="activity_type"[^>]*>.*?</select>`).FindString(body)
		if selectHTML == "" || strings.Contains(selectHTML, "selected") {
			t.Fatal("source invalid group type was not dropped")
		}
	})
	t.Run("flag_off_hides_link_and_redirects_nonstaff_create", func(t *testing.T) {
		s.Social.AllowUserGroups = false
		webCasePortAbsent(t, webCasePortHTML(t, mux, actor, "/communities/"), "Start a group")
		if w := webCasePortRead(mux, actor, "/groups/new/"); w.Code != 302 {
			t.Fatal("source create policy gate", w.Code)
		}
	})
}
