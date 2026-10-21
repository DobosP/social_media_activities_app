package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// TI-4: negative cases for the group-detail cohort gate and the adult-only
// roster gate. Each case renders an admitted control first and then applies one
// defect, so the refusal is attributable to the gate, not an incidental check.

type groupDetailGuardStep func(*http.Request, pongo2.Context) *http.Request

func groupDetailGuardActor(request *http.Request, data pongo2.Context, id int64, cohort string, staff bool) *http.Request {
	actor, _ := platform.ActorFrom(request)
	actor.ID, actor.Cohort, actor.IsStaff = id, cohort, staff
	data["user"] = socialActor(actor)
	return platform.WithActor(request, actor)
}

// A same-cohort nonmember who is shown only the static group metadata.
func groupDetailGuardStaticViewer(cohort string) groupDetailGuardStep {
	return func(request *http.Request, data pongo2.Context) *http.Request {
		request = groupDetailGuardActor(request, data, 12, cohort, false)
		data["group"].(map[string]any)["cohort"] = cohort
		data["is_owner"], data["is_member"], data["show_thread"], data["can_post"], data["can_announce"], data["show_dissent_concern"] = false, false, false, false, false, false
		data["roster"], data["posts"], data["announcements"], data["feed"] = nil, nil, nil, []map[string]any{}
		delete(data, "thread_chat_config")
		delete(data, "presend_nudge")
		return request
	}
}

// A same-cohort reader of the live thread, given no roster.
func groupDetailGuardLiveReader(cohort string, member bool) groupDetailGuardStep {
	return func(request *http.Request, data pongo2.Context) *http.Request {
		request = groupDetailGuardActor(request, data, 14, cohort, false)
		data["group"].(map[string]any)["cohort"] = cohort
		data["is_owner"], data["is_member"], data["can_announce"], data["can_post"], data["show_dissent_concern"] = false, member, false, false, false
		data["roster"], data["announcements"], data["feed"] = nil, nil, []map[string]any{}
		data["posts"] = []map[string]any{groupDetailFixturePost(91, groupDetailFixturePerson(15, "Generated peer"), "Current peer body", cohort)}
		data["thread_chat_config"] = groupDetailFixtureConfig(41, 51, 14)
		return request
	}
}

// An adult member whose thread the producer withheld.
func groupDetailGuardMemberWithoutThread(request *http.Request, data pongo2.Context) *http.Request {
	request = groupDetailGuardStaticViewer("adult")(request, data)
	data["is_member"] = true
	return request
}

func groupDetailGuardGroupCohort(cohort string) groupDetailGuardStep {
	return func(request *http.Request, data pongo2.Context) *http.Request {
		data["group"].(map[string]any)["cohort"] = cohort
		return request
	}
}

func groupDetailGuardRoster(rows ...map[string]any) groupDetailGuardStep {
	return func(request *http.Request, data pongo2.Context) *http.Request {
		data["roster"] = append([]map[string]any{}, rows...)
		return request
	}
}

func TestNativeGroupDetailRefusesCrossCohortAndNonAdultRosterProjections(t *testing.T) {
	peer := groupDetailFixturePerson(15, "PRIVATE-roster-peer")
	large := make([]map[string]any, 0, 1001)
	for i := int64(0); i < 1001; i++ {
		large = append(large, groupDetailFixturePerson(100+i, "Generated member"))
	}
	cases := map[string]struct{ control, defect groupDetailGuardStep }{
		// Cohort gate: a non-staff viewer never receives another cohort's group.
		"adult-viewer-teen-group":  {groupDetailGuardStaticViewer("adult"), groupDetailGuardGroupCohort("teen")},
		"adult-viewer-child-group": {groupDetailGuardStaticViewer("adult"), groupDetailGuardGroupCohort("child")},
		"teen-viewer-adult-group":  {groupDetailGuardStaticViewer("teen"), groupDetailGuardGroupCohort("adult")},
		"teen-viewer-child-group":  {groupDetailGuardStaticViewer("teen"), groupDetailGuardGroupCohort("child")},
		"child-viewer-teen-group":  {groupDetailGuardStaticViewer("child"), groupDetailGuardGroupCohort("teen")},
		// Roster gate: adult viewer, adult group, membership and the thread.
		"teen-member-roster":                 {groupDetailGuardLiveReader("teen", true), groupDetailGuardRoster(peer)},
		"child-member-roster":                {groupDetailGuardLiveReader("child", true), groupDetailGuardRoster(peer)},
		"teen-member-empty-roster":           {groupDetailGuardLiveReader("teen", true), groupDetailGuardRoster()},
		"adult-nonmember-live-roster":        {groupDetailGuardLiveReader("adult", false), groupDetailGuardRoster(peer)},
		"adult-member-without-thread-roster": {groupDetailGuardMemberWithoutThread, groupDetailGuardRoster(peer)},
		"oversized-adult-roster":             {groupDetailGuardRoster(large[:1000]...), groupDetailGuardRoster(large...)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r, request, data := groupDetailProjectionFixture(t)
			request = c.control(request, data)
			if _, err := r.renderGroupDetail(request, data); err != nil {
				t.Fatal("admitted control refused; the defect would not be attributable", err)
			}
			request = c.defect(request, data)
			if body, err := r.renderGroupDetail(request, data); err == nil || len(body) != 0 {
				t.Fatal("group-detail gate admitted a cross-cohort or non-adult roster projection")
			}
			response := httptest.NewRecorder()
			if err := r.Render(response, request, "web/group_detail.html", data); err == nil || response.Body.Len() != 0 {
				t.Fatal("ordinary renderer wrote a refused group-detail projection")
			}
		})
	}
}

// Staff may view an outside-cohort group only as its curator; a roster supplied
// there is refused by both the roster gate and the outside-cohort clamp.
func TestNativeGroupDetailOutsideCohortCuratorNeverReceivesARoster(t *testing.T) {
	r, request, data := groupDetailProjectionFixture(t)
	request = groupDetailGuardActor(request, data, 11, "adult", true)
	g := data["group"].(map[string]any)
	g["cohort"], g["is_staff_curated"], g["owner_is_staff"] = "child", true, true
	data["is_member"], data["can_post"], data["show_dissent_concern"], data["roster"], data["posts"], data["feed"] = false, false, false, nil, nil, []map[string]any{}
	delete(data, "thread_chat_config")
	delete(data, "presend_nudge")
	if _, err := r.renderGroupDetail(request, data); err != nil {
		t.Fatal("outside-cohort curator control refused", err)
	}
	for _, roster := range [][]map[string]any{{}, {groupDetailFixturePerson(14, "PRIVATE-minor-member")}} {
		data["roster"] = roster
		if body, err := r.renderGroupDetail(request, data); err == nil || len(body) != 0 {
			t.Fatal("outside-cohort curator received a child group's roster")
		}
	}
}
