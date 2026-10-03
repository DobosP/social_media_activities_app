package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func rewriteFixtureJSON(t *testing.T, dir, file string, change func(map[string]any)) {
	t.Helper()
	p := filepath.Join(dir, file)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	change(obj)
	raw, err = json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func resealFixture(t *testing.T, dir string) {
	t.Helper()
	rewriteFixtureJSON(t, dir, "manifest.json", func(m map[string]any) {
		for name, metadata := range m["datasets"].(map[string]any) {
			raw, err := os.ReadFile(filepath.Join(dir, name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			metadata.(map[string]any)["sha256"] = sha256Sum(raw)
		}
	})
}

func TestLoaderRejectsUnsafeRelease(t *testing.T) {
	tests := map[string]func(*testing.T, string){
		"path traversal": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "manifest.json", func(m map[string]any) {
				m["datasets"].(map[string]any)["events"].(map[string]any)["file"] = "../events.json"
			})
		},
		"unsupported schema": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "manifest.json", func(m map[string]any) { m["schema_version"] = 1 })
		},
		"missing dataset": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "manifest.json", func(m map[string]any) { delete(m["datasets"].(map[string]any), "activities") })
		},
		"count budget": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "manifest.json", func(m map[string]any) { m["datasets"].(map[string]any)["events"].(map[string]any)["count"] = 10001 })
		},
		"checksum": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "manifest.json", func(m map[string]any) {
				m["datasets"].(map[string]any)["events"].(map[string]any)["sha256"] = sha256Sum([]byte("wrong"))
			})
		},
		"generation mismatch": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "events.json", func(m map[string]any) { m["generated_at"] = "2026-07-13T10:00:00Z" })
			resealFixture(t, dir)
		},
		"count mismatch": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "events.json", func(m map[string]any) { m["count"] = 1 })
			resealFixture(t, dir)
		},
		"private field": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "events.json", func(m map[string]any) {
				m["records"].([]any)[0].(map[string]any)["owner_email"] = "never-export@example.invalid"
			})
			resealFixture(t, dir)
		},
		"allowlist phrase is not a field": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "events.json", func(m map[string]any) {
				m["records"].([]any)[0].(map[string]any)["id title"] = "never-export@example.invalid"
			})
			resealFixture(t, dir)
		},
		"private nested field": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "events.json", func(m map[string]any) {
				m["records"].([]any)[0].(map[string]any)["place_summary"].(map[string]any)["raw_tags"] = "private"
			})
			resealFixture(t, dir)
		},
		"object in scalar field": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "events.json", func(m map[string]any) {
				m["records"].([]any)[0].(map[string]any)["ends_at"] = map[string]any{"owner_email": "private"}
			})
			resealFixture(t, dir)
		},
		"object in string array": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "places.json", func(m map[string]any) {
				m["records"].([]any)[0].(map[string]any)["activity_types"] = []any{map[string]any{"owner_email": "private"}}
			})
			resealFixture(t, dir)
		},

		"duplicate approved key": func(t *testing.T, dir string) {
			p := filepath.Join(dir, "events.json")
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			raw = bytes.Replace(raw, []byte(`"title": "Chess`), []byte(`"title":{"owner_email":"private"},"title": "Chess`), 1)
			if err := os.WriteFile(p, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			resealFixture(t, dir)
		},
		"duplicate nested key": func(t *testing.T, dir string) {
			p := filepath.Join(dir, "events.json")
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			raw = bytes.Replace(raw, []byte(`"name": "Central Park"`), []byte(`"name":{"owner_email":"private"},"name": "Central Park"`), 1)
			if err := os.WriteFile(p, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			resealFixture(t, dir)
		},

		"case alias in taxonomy": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "taxonomy.json", func(m map[string]any) {
				m["Categories"] = m["categories"]
				m["categories"] = []any{map[string]any{"owner_email": "private"}}
			})
			resealFixture(t, dir)
		},
		"case alias in manifest": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "manifest.json", func(m map[string]any) {
				m["Licenses"] = m["licenses"]
				m["licenses"] = []any{map[string]any{"owner_email": "private"}}
			})
		},

		"minor activity": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "activities.json", func(m map[string]any) { m["records"].([]any)[0].(map[string]any)["cohort"] = "child" })
			resealFixture(t, dir)
		},
		"duplicate identity": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "events.json", func(m map[string]any) { m["records"].([]any)[1].(map[string]any)["id"] = 1 })
			resealFixture(t, dir)
		},
		"taxonomy private field": func(t *testing.T, dir string) {
			rewriteFixtureJSON(t, dir, "taxonomy.json", func(m map[string]any) { m["categories"].([]any)[0].(map[string]any)["owner"] = 123 })
			resealFixture(t, dir)
		},
		"symlink escape": func(t *testing.T, dir string) {
			p := filepath.Join(dir, "events.json")
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(t.TempDir(), "outside.json")
			if err := os.WriteFile(external, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, p); err != nil {
				t.Fatal(err)
			}
			rewriteFixtureJSON(t, dir, "manifest.json", func(m map[string]any) { m["site"] = "https://example.org/changed" })
		},
		"byte budget": func(t *testing.T, dir string) {
			p := filepath.Join(dir, "events.json")
			if err := os.Truncate(p, snapshotByteCaps["events"]+1); err != nil {
				t.Fatal(err)
			}
			rewriteFixtureJSON(t, dir, "manifest.json", func(m map[string]any) { m["site"] = "https://example.org/changed" })
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			loader, dir := newLoadedLoader(t)
			good := loader.Current()
			mutate(t, dir)
			changed, err := loader.CheckReload()
			if err == nil || changed {
				t.Fatalf("unsafe release accepted: changed=%v err=%v", changed, err)
			}
			if loader.Current() != good {
				t.Fatal("unsafe snapshot replaced last validated release")
			}
		})
	}
}

func TestLoaderContentDigestChangesWithEqualCountsAndGeneration(t *testing.T) {
	loader, dir := newLoadedLoader(t)
	before := loader.Current()
	info, err := os.Stat(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	rewriteFixtureJSON(t, dir, "events.json", func(m map[string]any) { m["records"].([]any)[0].(map[string]any)["title"] = "Updated approved title" })
	resealFixture(t, dir)
	if err := os.Chtimes(filepath.Join(dir, "manifest.json"), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	changed, err := loader.CheckReload()
	if err != nil || !changed {
		t.Fatalf("content change missed: %v %v", changed, err)
	}
	if before.version == loader.Current().version {
		t.Fatal("ETag release identity did not include dataset bytes")
	}
	if before.GeneratedAt != loader.Current().GeneratedAt {
		t.Fatal("test must preserve generation")
	}
	loadedAt := loader.Current().LoadedAt
	if changed, err := loader.CheckReload(); err != nil || changed {
		t.Fatalf("unchanged release reloaded: %v %v", changed, err)
	}
	if loader.Current().LoadedAt != loadedAt {
		t.Fatal("unchanged release reset load age")
	}
}
