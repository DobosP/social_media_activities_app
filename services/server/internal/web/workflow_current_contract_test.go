package web

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// The action error/success producer supplies an empty CSRF string when Auth is
// absent. Rendering must preserve that existing value rather than replace the
// producer's retryable/error/safe-exit result with a projection failure.
func TestNativeWorkflowActionMessagesKeepSuppliedCSRF(t *testing.T) {
	for _, tc := range []struct{ name, cohort string }{
		{"web/activity_detail.html", "adult"},
		{"web/activity_safe_exit.html", "adult"},
		{"web/activity_safe_exit.html", "child"},
	} {
		t.Run(tc.name+"/"+tc.cohort, func(t *testing.T) {
			name, cohort := tc.name, tc.cohort
			r, request, data := workflowsFixture(t, name)
			if cohort == "child" {
				actor, _ := platform.ActorFrom(request)
				actor.Cohort, actor.AgeBand = "child", "under13"
				request = platform.WithActor(request, actor)
				data["user"] = socialActor(actor)
			}
			request.Method = "POST"
			data["csrf"] = ""
			data["messages"] = []socialMessage{{"tags": "error", "message": "Private processing unavailable <script>message</script>. You can leave this activity any time."}}
			before, _ := json.Marshal(data)
			raw, err := r.renderWorkflows(request, name, data)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(data)
			if !bytes.Equal(before, after) {
				t.Fatal("mutated the action producer snapshot")
			}
			body := string(raw)
			for _, want := range []string{`name="csrfmiddlewaretoken" value=""`, "Private processing unavailable &lt;script&gt;message&lt;/script&gt;", "You can leave this activity any time."} {
				if !strings.Contains(body, want) {
					t.Fatalf("lost action error/safe-exit contract %s", want)
				}
			}
			if strings.Contains(body, "<script>message") || strings.Contains(body, "PRIVATE-owner") || strings.Contains(body, "PRIVATE-logistics") {
				t.Fatal("unsafe message or minimal safe-exit source leaked")
			}
		})
	}
}

func TestNativeWorkflowReferenceMediaMarkerIsOptional(t *testing.T) {
	for _, videos := range []bool{false, true} {
		name := "web/activity_detail.html"
		r, request, data := workflowsFixture(t, name)
		delete(data, "media_capabilities_supplied")
		data["attachments_enabled"], data["file_enabled"], data["attachment_accept"] = false, false, ""
		data["video_enabled"] = videos
		data["ephemeral_options"] = []any{[]string{"3600", "1 hour"}, []string{"86400", "1 day"}, []string{"604800", "1 week"}}
		raw, err := r.renderWorkflows(request, name, data)
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		accept := "image/png,image/jpeg,image/webp,application/pdf"
		if videos {
			accept += ",video/mp4,video/quicktime,video/webm"
		}
		for _, want := range []string{`name="attachment"`, `accept="` + accept + `"`, `name="disappear"`, `value="3600"`, `value="86400"`, `value="604800"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("lost reference media contract %s", want)
			}
		}
		help := regexp.MustCompile(`(?s)<p class="muted compose-upload-note">(.*?)</p>`).FindStringSubmatch(body)
		if len(help) != 2 {
			t.Fatal("reference composer lost its specific privacy/screening guidance")
		}
		plainHelp := mediaComposerPlain(help[1])
		if !strings.Contains(plainHelp, "private to members") || !strings.Contains(plainHelp, "safety-screened on upload") {
			t.Fatal("reference composer lost its private/screened media guidance", plainHelp)
		}
		data["media_capabilities_supplied"] = "true"
		if raw, err = r.renderWorkflows(request, name, data); err == nil || len(raw) != 0 {
			t.Fatal("accepted a mistyped capability marker")
		}
	}
}

func TestNativeWorkflowMemberCopyKeepsApostrophesAndEscapesMarkup(t *testing.T) {
	r, request, data := workflowsFixture(t, "web/activity_detail.html")
	data["my_transit"], data["arrival_window_open"] = "on_my_way", true
	raw, err := r.renderWorkflows(request, "web/activity_detail.html", data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"you're on the way", "I've arrived"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("lost source member-control text %s", want)
		}
	}
	const message = "I've arrived"
	page := workflowPage{Copy: map[string]string{message: "I've arrived <script>copy</script> & \"quoted\""}}
	if value := string(page.Text(message)); value != "I've arrived &lt;script&gt;copy&lt;/script&gt; &amp; &#34;quoted&#34;" {
		t.Fatal("text copy admitted markup or lost literal apostrophe", value)
	}
}
