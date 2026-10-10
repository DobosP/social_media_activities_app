package web

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/flosch/pongo2/v6"
)

var guiPublicGraphScriptPaths = []string{
	"/static/js/vendor/3d-force-graph.min.js",
	"/static/js/community-graph.js",
}

func guiPublicGraphNonceBinding(body, policy string) (string, error) {
	base, err := guiPublicScriptNonceBinding(body, policy, nil)
	if err != nil {
		return "", err
	}
	// The existing base-script checker requires exactly one real, nonempty
	// script-src nonce and verifies it against the native base scripts.
	var nonce string
	for _, directive := range strings.Split(policy, ";") {
		fields := strings.Fields(directive)
		if len(fields) == 0 || fields[0] != "script-src" {
			continue
		}
		for _, token := range fields[1:] {
			if strings.HasPrefix(token, "'nonce-") && strings.HasSuffix(token, "'") {
				nonce = strings.TrimSuffix(strings.TrimPrefix(token, "'nonce-"), "'")
			}
		}
	}
	if nonce == "" || guiPublicHash([]byte(nonce)) != base["header_nonce_sha256"] {
		return "", fmt.Errorf("native graph/base header nonce differs")
	}
	previous := -1
	for _, path := range guiPublicGraphScriptPaths {
		tag := `<script nonce="` + nonce + `" src="` + path + `" defer>`
		position := strings.Index(body, tag)
		if strings.Count(body, tag) != 1 || strings.Count(body, `src="`+path+`"`) != 1 || position <= previous {
			return "", fmt.Errorf("graph script nonce/path/order/defer binding differs")
		}
		previous = position
	}
	return nonce, nil
}

func guiPublicGraphNonceResponse(t *testing.T, filesystem, populated bool) (string, string) {
	t.Helper()
	root, err := filepath.Abs("../../../../")
	if err != nil {
		t.Fatal(err)
	}
	const graphPath = "apps/web/templates/web/communities_graph.html"
	input, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	file, err := input.Open(graphPath)
	input.Close()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := readRendererFSFile(file, 512<<10)
	if err != nil {
		t.Fatal(err)
	}
	graphHash := guiPublicHash(raw)
	t.Cleanup(func() {
		after, err := os.ReadFile(filepath.Join(root, graphPath))
		if err != nil || guiPublicHash(after) != graphHash {
			t.Error("actual graph template changed during native renderer checks")
		}
	})
	renderer := NewRenderer(root)
	if filesystem {
		snapshot, err := guiPublicFilesystemSnapshot(root)
		if err != nil {
			t.Fatal(err)
		}
		// A fresh caller-owned snapshot adds the exact graph template. This is
		// not an embedded release or an immutability claim about arbitrary FS.
		files := fstest.MapFS{}
		for name, file := range snapshot.files {
			files[name] = file
		}
		files[graphPath] = &fstest.MapFile{Data: append([]byte(nil), raw...), Mode: 0o444}
		t.Cleanup(func() {
			if err := snapshot.check(); err != nil {
				t.Error(err)
			}
			if len(files) != 7 || files[graphPath] == nil || files[graphPath].Mode != 0o444 || guiPublicHash(files[graphPath].Data) != graphHash {
				t.Error("graph FS snapshot member/bytes/mode changed")
			}
			for name, file := range snapshot.files {
				if files[name] != file {
					t.Error("original public FS snapshot member replaced")
				}
			}
		})
		renderer, err = NewRendererFS(root, files)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Synthetic anonymous presentation only: no accounts, DB, cohort admission,
	// graph API execution, library execution or child data is involved.
	communities := []any{}
	if populated {
		communities = append(communities, map[string]any{"slug": "synthetic-public-community", "name": "Synthetic public community"})
	}
	request := httptest.NewRequest("GET", "https://gui-fixture.invalid/communities/graph/", nil)
	request.Header.Set("Accept-Language", "en")
	data := pongo2.Context{"communities": communities}
	seen := map[string]bool{}
	var firstBody, firstPolicy string
	for iteration := 0; iteration < 2; iteration++ {
		response := httptest.NewRecorder()
		if err := renderer.Render(response, request, "web/communities_graph.html", data); err != nil {
			t.Fatal(err)
		}
		body := response.Body.String()
		policy := response.Header().Get("Content-Security-Policy-Report-Only")
		if response.Code != 200 || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || policy == "" || response.Header().Get("Content-Security-Policy") != "" {
			t.Fatal("existing graph native response/report-only mode changed")
		}
		nonce, err := guiPublicGraphNonceBinding(body, policy)
		if err != nil {
			t.Fatal(err)
		}
		digest := guiPublicHash([]byte(nonce))
		if seen[digest] {
			t.Fatal("native graph renderer reused a nonce across responses")
		}
		seen[digest] = true
		if populated {
			if strings.Count(body, `<a href="/communities/synthetic-public-community/">Synthetic public community</a>`) != 1 || strings.Contains(body, "No communities yet.") {
				t.Fatal("actual populated community list arm changed")
			}
		} else if strings.Count(body, "No communities yet.") != 1 || strings.Contains(body, "Synthetic public community") {
			t.Fatal("actual empty community list arm changed")
		}
		if iteration == 0 {
			firstBody, firstPolicy = body, policy
		}
	}
	return firstBody, firstPolicy
}

func TestGUIPublicGraphNonceFreshNativeResponses(t *testing.T) {
	for _, filesystem := range []bool{false, true} {
		for _, populated := range []bool{false, true} {
			t.Run(fmt.Sprintf("filesystem-%t/populated-%t", filesystem, populated), func(t *testing.T) {
				guiPublicGraphNonceResponse(t, filesystem, populated)
			})
		}
	}
}

func TestGUIPublicGraphNonceRejectsRenderedMutations(t *testing.T) {
	body, policy := guiPublicGraphNonceResponse(t, false, false)
	nonce, err := guiPublicGraphNonceBinding(body, policy)
	if err != nil {
		t.Fatal(err)
	}
	for index, path := range guiPublicGraphScriptPaths {
		tag := `<script nonce="` + nonce + `" src="` + path + `" defer>`
		for _, mutation := range []struct {
			name string
			tag  string
		}{
			{"missing-nonce", `<script src="` + path + `" defer>`},
			{"wrong-nonce", strings.Replace(tag, nonce, "wrong-"+nonce, 1)},
			{"duplicate", tag + `</script>` + tag},
			{"changed-path", strings.Replace(tag, path, "/static/js/synthetic-different.js", 1)},
			{"changed-defer", strings.Replace(tag, " defer>", " async>", 1)},
		} {
			t.Run(fmt.Sprintf("script-%d/%s", index, mutation.name), func(t *testing.T) {
				changed := strings.Replace(body, tag, mutation.tag, 1)
				if changed == body {
					t.Fatal("rendered negative control did not change the actual tag")
				}
				if _, err := guiPublicGraphNonceBinding(changed, policy); err == nil {
					t.Fatal("actual graph script mutation passed nonce/path/order/defer checks")
				}
			})
		}
	}
	t.Run("missing-header", func(t *testing.T) {
		if _, err := guiPublicGraphNonceBinding(body, ""); err == nil {
			t.Fatal("missing native header passed graph binding")
		}
	})
	t.Run("disagreeing-header", func(t *testing.T) {
		changed := strings.Replace(policy, "'nonce-"+nonce+"'", "'nonce-wrong-"+nonce+"'", 1)
		if changed == policy {
			t.Fatal("header negative control did not change the actual nonce")
		}
		if _, err := guiPublicGraphNonceBinding(body, changed); err == nil {
			t.Fatal("disagreeing native header passed graph binding")
		}
	})
	t.Run("reversed-script-order", func(t *testing.T) {
		first := `<script nonce="` + nonce + `" src="` + guiPublicGraphScriptPaths[0] + `" defer>`
		second := `<script nonce="` + nonce + `" src="` + guiPublicGraphScriptPaths[1] + `" defer>`
		const marker = "<!-- synthetic-script-order-control -->"
		changed := strings.Replace(body, first, marker, 1)
		changed = strings.Replace(changed, second, first, 1)
		changed = strings.Replace(changed, marker, second, 1)
		if changed == body {
			t.Fatal("order negative control did not change the actual body")
		}
		if _, err := guiPublicGraphNonceBinding(changed, policy); err == nil {
			t.Fatal("reversed graph script order passed binding")
		}
	})
}
