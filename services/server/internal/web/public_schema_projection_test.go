package web

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// SQL rows lose their Go type at json.RawMessage. Compare the reviewed field
// contracts against the actual outer jsonb_build_object keys, not a copy of
// schema input. Optional fields added by native serializers are separate.
func TestNativeOpenAPISQLProjectionFields(t *testing.T) {
	schemas := map[string]any{}
	privateSchemas(schemas)
	bindings := []struct {
		source, symbol, schema string
		extra                  []string
	}{
		{"social/service.go", "activityColumns", "Activity", nil},
		{"social/service.go", "membershipColumns", "Membership", nil},
		{"social/groups.go", "groupColumns", "Group", nil},
		{"social/series.go", "seriesColumns", "Series", nil},
		{"social/gauges.go", "gaugeColumns", "Gauge", nil},
		{"social/proposals.go", "proposalColumns", "PlaceProposal", nil},
		{"social/connections.go", "connectionProjection", "Connection", nil},
		{"social/connections.go", "userRef", "UserReference", nil},
		{"social/communities.go", "communityColumns", "Community", nil},
		{"social/threads.go", "postProjection", "Post", nil},
		{"booking/service.go", "projection", "Booking", nil},
		{"donations/service.go", "projection", "Donation", nil},
		{"recommendations/saved_searches.go", "savedProjection", "SavedSearch", nil},
		{"safety/queues.go", "appealProjection", "ModeratorAppeal", nil},
		{"discovery/service.go", "card", "DiscoveryActivity", []string{"visual", "reason", "match_score"}},
		{"discovery/feeds.go", "eventCard", "DiscoveryEvent", []string{"reason"}},
		{"catalog/service.go", "eventProjection", "Event", nil},
	}
	full := (&Server{}).publicSchema()["components"].(map[string]any)["schemas"].(map[string]any)
	for _, binding := range bindings {
		t.Run(binding.schema, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", binding.source), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]ast.Expr{}
			for _, decl := range file.Decls {
				if g, ok := decl.(*ast.GenDecl); ok {
					for _, spec := range g.Specs {
						if v, ok := spec.(*ast.ValueSpec); ok {
							for i, name := range v.Names {
								if i < len(v.Values) {
									values[name.Name] = v.Values[i]
								}
							}
						}
					}
				}
			}
			expr := values[binding.symbol]
			if expr == nil {
				t.Fatal("projection source not found")
			}
			query := sqlProjectionText(expr, values, 0)
			expected := outerJSONKeys(t, query)
			shape := full[binding.schema].(map[string]any)
			props := schemaProperties(t, shape, full)
			actual := []string{}
			for key := range props {
				actual = append(actual, key)
			}
			expected = append(expected, binding.extra...)
			sort.Strings(expected)
			sort.Strings(actual)
			if !reflect.DeepEqual(expected, actual) {
				t.Errorf("projection fields differ: source=%v schema=%v", expected, actual)
			}
		})
	}
	// Inline portability projections are independently grounded in the source
	// rather than allowing an arbitrary object for every export section.
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "accounts", "export.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	exports := map[string]string{"profile": "ExportProfile", "api_access": "ExportAPIAccess", "privacy_settings": "ExportPrivacy", "donations": "ExportDonations", "age_assurance": "ExportAgeAssurance", "memberships": "ExportMembership", "owned_activities": "ExportActivity", "owned_groups": "ExportGroup", "group_memberships": "ExportGroupMembership", "blocks": "ExportBlock"}
	seen := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || len(lit.Elts) != 2 {
			return true
		}
		key, ok := lit.Elts[0].(*ast.BasicLit)
		if !ok || key.Kind != token.STRING {
			return true
		}
		name, _ := strconv.Unquote(key.Value)
		schema, exists := exports[name]
		if !exists {
			return true
		}
		query := sqlProjectionText(lit.Elts[1], nil, 0)
		keys := outerJSONKeys(t, query)
		props := schemaProperties(t, schemas[schema].(map[string]any), schemas)
		actual := []string{}
		for key := range props {
			actual = append(actual, key)
		}
		sort.Strings(keys)
		sort.Strings(actual)
		if !reflect.DeepEqual(keys, actual) {
			t.Errorf("export section %s differs: source=%v schema=%v", name, keys, actual)
		}
		seen[name] = true
		return true
	})
	for name := range exports {
		if !seen[name] {
			t.Errorf("export section source not validated: %s", name)
		}
	}
}

func sqlProjectionText(expr ast.Expr, values map[string]ast.Expr, depth int) string {
	if depth > 30 {
		return "NULL"
	}
	switch x := expr.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			v, _ := strconv.Unquote(x.Value)
			return v
		}
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			return sqlProjectionText(x.X, values, depth+1) + sqlProjectionText(x.Y, values, depth+1)
		}
	case *ast.Ident:
		if child := values[x.Name]; child != nil {
			return sqlProjectionText(child, values, depth+1)
		}
	}
	return "NULL"
}

func outerJSONKeys(t *testing.T, query string) []string {
	t.Helper()
	start := strings.Index(query, "jsonb_build_object(")
	if start < 0 {
		t.Fatal("not a JSON projection")
	}
	start += len("jsonb_build_object(")
	args, depth, quoted, begin := []string{}, 0, false, start
	for i := start; i < len(query); i++ {
		ch := query[i]
		if ch == '\'' {
			if quoted && i+1 < len(query) && query[i+1] == '\'' {
				i++
				continue
			}
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		switch ch {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				args = append(args, strings.TrimSpace(query[begin:i]))
				keys := []string{}
				if len(args)%2 != 0 {
					t.Fatalf("odd JSON projection arguments: %v", args)
				}
				for j := 0; j < len(args); j += 2 {
					if !strings.HasPrefix(args[j], "'") || !strings.HasSuffix(args[j], "'") {
						t.Fatalf("dynamic projection key %s", args[j])
					}
					keys = append(keys, strings.Trim(args[j], "'"))
				}
				return keys
			}
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(query[begin:i]))
				begin = i + 1
			}
		}
	}
	t.Fatal("unterminated JSON projection")
	return nil
}
