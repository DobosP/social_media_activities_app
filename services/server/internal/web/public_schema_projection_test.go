package web

import (
	"fmt"
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
			query, err := sourceProjectionText(file, binding.symbol)
			if err != nil {
				t.Fatal(err)
			}
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

// Resolve the bound symbol itself, whether it is a static value or a policy-aware
// builder. Each builder must have one direct return; branches cannot silently
// replace an independently checked projection with a union of field names.
func sourceProjectionText(file *ast.File, symbol string) (string, error) {
	values := map[string]ast.Expr{}
	functions := map[string]*ast.FuncDecl{}
	bind := func(decl *ast.GenDecl) {
		for _, spec := range decl.Specs {
			if v, ok := spec.(*ast.ValueSpec); ok {
				for i, name := range v.Names {
					if i < len(v.Values) {
						values[name.Name] = v.Values[i]
					}
				}
			}
		}
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			bind(d)
		case *ast.FuncDecl:
			if functions[d.Name.Name] != nil {
				return "", fmt.Errorf("ambiguous projection builder %s", d.Name.Name)
			}
			functions[d.Name.Name] = d
		}
	}
	expr := values[symbol]
	if expr == nil {
		builder := functions[symbol]
		if builder == nil || builder.Body == nil {
			return "", fmt.Errorf("projection source not found: %s", symbol)
		}
		returns := 0
		ast.Inspect(builder.Body, func(node ast.Node) bool {
			if _, literal := node.(*ast.FuncLit); literal {
				return false
			}
			if _, ok := node.(*ast.ReturnStmt); ok {
				returns++
			}
			return true
		})
		if returns != 1 {
			return "", fmt.Errorf("projection builder %s must have one return", symbol)
		}
		for _, statement := range builder.Body.List {
			switch stmt := statement.(type) {
			case *ast.AssignStmt:
				if len(stmt.Lhs) != len(stmt.Rhs) {
					return "", fmt.Errorf("unsupported projection assignment in %s", symbol)
				}
				for i, left := range stmt.Lhs {
					if name, ok := left.(*ast.Ident); ok {
						values[name.Name] = stmt.Rhs[i]
					}
				}
			case *ast.DeclStmt:
				if decl, ok := stmt.Decl.(*ast.GenDecl); ok {
					bind(decl)
				}
			case *ast.ReturnStmt:
				if len(stmt.Results) == 1 {
					expr = stmt.Results[0]
				}
			default:
				return "", fmt.Errorf("unsupported projection builder statement in %s", symbol)
			}
		}
		if expr == nil {
			return "", fmt.Errorf("projection builder %s needs a direct SQL return", symbol)
		}
	}
	query := sqlProjectionText(expr, values, 0)
	if !strings.Contains(query, "jsonb_build_object(") {
		return "", fmt.Errorf("projection %s is not a supported SQL expression", symbol)
	}
	return query, nil
}

// Numeric policy substitutions affect SQL values, not field names. Restrict
// formatting to %d outside SQL quoted strings and preserve literal %% exactly;
// unsupported/dynamic key formatting must fail the source projection check.
func numericProjectionTemplate(template string, argumentCount int) (string, bool) {
	var out strings.Builder
	quoted, substitutions := false, 0
	for i := 0; i < len(template); i++ {
		ch := template[i]
		if ch == '\'' {
			if quoted && i+1 < len(template) && template[i+1] == '\'' {
				out.WriteString("''")
				i++
				continue
			}
			quoted = !quoted
		}
		if ch == '%' {
			if i+1 >= len(template) {
				return "", false
			}
			i++
			switch template[i] {
			case '%':
				out.WriteByte('%')
			case 'd':
				if quoted {
					return "", false
				}
				substitutions++
				out.WriteByte('0')
			default:
				return "", false
			}
			continue
		}
		out.WriteByte(ch)
	}
	return out.String(), substitutions == argumentCount && !quoted
}

func TestNativeOpenAPIProjectionBuilders(t *testing.T) {
	for _, fixture := range []struct {
		name, source string
		keys         []string
	}{
		{"constant", "const projection = `jsonb_build_object('id',p.id,'title',p.title)`", []string{"id", "title"}},
		{"numeric-policy-template", "const template = `jsonb_build_object('id',g.id,'ready',COUNT(*)>=%d,'remaining',GREATEST(0,%d-COUNT(*)))`; func (s *Service) projection() string { return fmt.Sprintf(template,s.Policy.Threshold,s.Policy.Threshold) }", []string{"id", "ready", "remaining"}},
		{"local-policy-and-nested-share", "const shared = `wrong`; func projection(ctx Context) string { predicate := policy(ctx).PlaceSQL(); shared := `CASE WHEN (` + predicate + `) THEN jsonb_build_object('kind','place','label','Reader''s venue') ELSE NULL END`; return `jsonb_build_object('id',p.id,'share',` + shared + `,'body',p.body)` }", []string{"id", "share", "body"}},
		{"local-declaration", "func projection() string { const prefix = `jsonb_build_object('id',p.id`; return (prefix + `,'name',p.name)`) }", []string{"id", "name"}},
		{"literal-percent", "func projection() string { return fmt.Sprintf(`jsonb_build_object('value','100%%','ready',n>=%d)`,threshold) }", []string{"value", "ready"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", "package fixture\n"+fixture.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			query, err := sourceProjectionText(file, "projection")
			if err != nil {
				t.Fatal(err)
			}
			if got := outerJSONKeys(t, query); !reflect.DeepEqual(got, fixture.keys) {
				t.Fatalf("returned projection keys=%v want=%v", got, fixture.keys)
			}
		})
	}
	for _, source := range []string{
		"func missing() string { return `jsonb_build_object('id',p.id)` }",
		"func projection() string { if condition { return `jsonb_build_object('id',p.id)` }; return `jsonb_build_object('private',p.secret)` }",
		"func projection() string { return unsupportedBuilder() }",
		"func projection() string { return fmt.Sprintf(`jsonb_build_object('%s',p.id)`,key) }",
		"func projection() string { return fmt.Sprintf(`jsonb_build_object('key_%d',p.id)`,threshold) }",
		"func projection() string { return fmt.Sprintf(`jsonb_build_object('id',p.id,'ready',n>=%d)`) }",
		"func projection() string { return fmt.Sprintf(`jsonb_build_object('id',p.id,'ready',n>=%s)`,threshold) }",
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", "package fixture\n"+source, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sourceProjectionText(file, "projection"); err == nil {
			t.Fatal("unsupported/dynamic projection silently accepted", source)
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
	case *ast.ParenExpr:
		return sqlProjectionText(x.X, values, depth+1)
	case *ast.CallExpr:
		if selector, ok := x.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Sprintf" && len(x.Args) > 0 {
			if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "fmt" {
				if query, valid := numericProjectionTemplate(sqlProjectionText(x.Args[0], values, depth+1), len(x.Args)-1); valid {
					return query
				}
			}
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
