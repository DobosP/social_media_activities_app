package web

import (
	"bytes"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
)

func TestNativeGroupDetailSuppliedAskFlagDoesNotRecomputeThreadMembership(t *testing.T) {
	r, request, data := groupDetailProjectionFixture(t)
	actor, _ := platform.ActorFrom(request)
	actor.ID, actor.IsStaff = 13, true
	request = platform.WithActor(request, actor)
	data["user"] = socialActor(actor)
	g := data["group"].(map[string]any)
	g["cohort"], g["is_staff_curated"], g["owner_is_staff"] = "child", true, true
	data["is_owner"], data["is_member"], data["show_thread"], data["can_post"], data["can_announce"], data["show_dissent_concern"] = false, false, false, false, false, false
	data["can_ask"] = true
	data["roster"], data["posts"], data["announcements"], data["feed"] = nil, nil, nil, []map[string]any{}
	delete(data, "thread_chat_config")
	delete(data, "presend_nudge")
	prompts := []any{}
	for _, slug := range []string{"next_meetup", "where", "what_to_bring", "how_it_works", "more_info"} {
		prompts = append(prompts, []string{slug, social.GroupPrompts[slug]})
	}
	data["question_prompts"] = prompts
	page, err := r.groupDetailPage(request, data)
	if err != nil || !page.CanAsk || page.Member || page.ShowThread || page.LiveThread || page.CanPost || page.RosterVisible {
		t.Fatal("native view recomputed the supplied question flag from private thread permission", err)
	}
	body, err := r.renderGroupDetail(request, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`action="/groups/41/ask/"`, `name="prompt"`, `value="next_meetup"`, `value="more_info"`, "Generated standing group"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatal("existing staff metadata/question affordance disappeared", want)
		}
	}
	for _, forbidden := range []string{"Current body", "Current reply", "Generated member", "Own announcement", "Members (", `id="compose"`, `thread-chat-config`, `/static/js/thread-chat.js`, `action="/groups/41/archive/"`} {
		if bytes.Contains(body, []byte(forbidden)) {
			t.Fatal("retained raw membership created a private read or owner capability", forbidden)
		}
	}
}
