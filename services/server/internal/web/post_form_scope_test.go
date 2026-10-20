package web

import (
	"fmt"
	"maps"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

type postFormScopeSafeMarkup struct{ Markup string }

// Pongo values contain reflect.Value pointers; compare their complete rendered
// value and preserve a distinct safe-markup type rather than pointer identity.
func postFormScopeSemantic(value any) any {
	switch current := value.(type) {
	case *pongo2.Value:
		if current == nil {
			return current
		}
		return postFormScopeSafeMarkup{Markup: current.String()}
	case map[string]any:
		out := make(map[string]any, len(current))
		for key, item := range current {
			out[key] = postFormScopeSemantic(item)
		}
		return out
	case []any:
		out := make([]any, len(current))
		for i, item := range current {
			out[i] = postFormScopeSemantic(item)
		}
		return out
	default:
		return value
	}
}

func TestPostFormWorkflowMetadataRequiresActivityConstructor(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		t.Run(method, func(t *testing.T) {
			// Neither PostForm constructor requires a database. This exercises the
			// real shared form producer, rather than a synthetic snapshot helper.
			s := &Server{}
			actor := platform.Actor{ID: 981, Cohort: "adult"}
			request := httptest.NewRequest(method, "/activities/29/", nil)
			initial := map[string]any{"body": "Initial <message>", "reply_to": int64(61)}
			if method == "POST" {
				request.PostForm = url.Values{"body": {"first", "Bound </textarea><script>message</script>"}, "reply_to": {"62", "99"}, "ping": {"on"}}
			}
			ordinary, err := s.form(request, actor, "PostForm", initial)
			if err != nil {
				t.Fatal(err)
			}
			if _, present := ordinary["workflow_native"]; present {
				t.Fatal("ordinary/group PostForm constructed unused workflow metadata")
			}
			activity, err := s.activityPostForm(request, actor, initial)
			if err != nil {
				t.Fatal(err)
			}
			// Both actual thread contexts apply this existing repair after their
			// chosen constructor; only the activity copy carries the typed form.
			socialBoundFormRepair(request, ordinary, initial, false)
			socialBoundFormRepair(request, activity, initial, false)
			legacyActivity := maps.Clone(activity)
			delete(legacyActivity, "workflow_native")
			if !reflect.DeepEqual(postFormScopeSemantic(ordinary), postFormScopeSemantic(legacyActivity)) {
				t.Fatal("scoping metadata changed the established BoundField values/widgets")
			}
			snapshot, ok := activity["workflow_native"].(workflowForm)
			if !ok || snapshot.Name != "PostForm" || len(snapshot.Fields) != len(definitions["PostForm"]) {
				t.Fatal("explicit activity constructor lost the complete typed form")
			}
			body, reply := "Initial <message>", "61"
			if method == "POST" {
				body, reply = "Bound </textarea><script>message</script>", "62"
			}
			if field := snapshot.Field("body"); field.Type != "textarea" || field.Value != body {
				t.Fatal("activity body binding differs from the producer's current value")
			}
			if field := snapshot.Field("reply_to"); field.Type != "hidden" || field.Value != reply {
				t.Fatal("activity quote target lost existing first-value binding")
			}
			widget := ordinary["body"].(map[string]any)["widget"]
			if strings.Contains(fmt.Sprint(widget), "<script>") {
				t.Fatal("ordinary post widget lost escaping")
			}
		})
	}
}
