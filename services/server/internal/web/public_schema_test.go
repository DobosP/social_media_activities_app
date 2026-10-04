package web

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestNativeOpenAPIRoutesAndFieldCoverage(t *testing.T) {
	doc := (&Server{}).publicSchema()
	paths := doc["paths"].(map[string]any)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	var inventory []struct{ Method, Path, Source string }
	if err := json.Unmarshal(nativeRouteInventory, &inventory); err != nil {
		t.Fatal(err)
	}
	actual, expected, sources := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, route := range inventory {
		if !strings.HasPrefix(route.Path, "/api/") {
			continue
		}
		key := route.Method + " " + route.Path
		if expected[key] {
			t.Fatalf("duplicate inventory operation %s", key)
		}
		expected[key], sources[route.Source] = true, true
		item, ok := paths[route.Path].(map[string]any)
		if !ok {
			t.Fatalf("missing path %s", key)
		}
		op, ok := item[strings.ToLower(route.Method)].(map[string]any)
		if !ok {
			t.Fatalf("missing operation %s", key)
		}
		responses := op["responses"].(map[string]any)
		success := false
		for status, value := range responses {
			if !strings.HasPrefix(status, "2") && !strings.HasPrefix(status, "3") {
				continue
			}
			success = true
			response := value.(map[string]any)
			if status == "204" || strings.HasPrefix(status, "3") {
				if response["content"] != nil {
					t.Errorf("bodyless response has content: %s status %s", key, status)
				}
			} else if content, ok := response["content"].(map[string]any); !ok || len(content) == 0 {
				t.Errorf("missing success field schema: %s status %s", key, status)
			}
		}
		if !success {
			t.Errorf("no success status: %s", key)
		}
	}
	// Discover HTTP registrars independently of the inventory, so an entirely
	// new route file/domain cannot silently evade the bidirectional check.
	registrarFiles, err := filepath.Glob(filepath.Join("..", "*", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	registrarFiles = append(registrarFiles, filepath.Join("..", "..", "..", "authcore", "service.go"))
	sourceRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, filename := range registrarFiles {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			f, ok := decl.(*ast.FuncDecl)
			if !ok || (f.Name.Name != "Register" && f.Name.Name != "registerSavedSearches") {
				continue
			}
			for _, parameter := range f.Type.Params.List {
				pointer, ok := parameter.Type.(*ast.StarExpr)
				if !ok {
					continue
				}
				selector, ok := pointer.X.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "ServeMux" {
					continue
				}
				pkg, ok := selector.X.(*ast.Ident)
				if !ok || pkg.Name != "http" {
					continue
				}
				absolute, err := filepath.Abs(filename)
				if err != nil {
					t.Fatal(err)
				}
				source, err := filepath.Rel(sourceRoot, absolute)
				if err != nil {
					t.Fatal(err)
				}
				sources[source] = true
			}
		}
	}
	for source := range sources {
		filename := filepath.Join("..", "..", source)
		file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		functions := map[string]*ast.FuncDecl{}
		for _, decl := range file.Decls {
			if f, ok := decl.(*ast.FuncDecl); ok {
				functions[f.Name.Name] = f
			}
		}
		register := functions["Register"]
		if strings.Contains(source, "saved_searches.go") {
			register = functions["registerSavedSearches"]
		}
		if register == nil {
			t.Fatalf("no route registrar in %s", source)
		}
		walkRouteStatements(t, register.Body.List, map[string]string{"prefix": "/api/auth"}, functions, actual)
		// Saved-search registration is deliberately in another file.

	}
	for key := range actual {
		if !expected[key] {
			t.Errorf("registered operation absent from inventory/schema: %s", key)
		}
	}
	for key := range expected {
		if !actual[key] {
			t.Errorf("stale operation in inventory/schema: %s", key)
		}
	}
	for path, value := range paths {
		for method := range value.(map[string]any) {
			if !expected[strings.ToUpper(method)+" "+path] {
				t.Errorf("schema advertises unregistered operation: %s %s", method, path)
			}
		}
	}
	if len(expected) != doc["x-native-api-operation-count"].(int) {
		t.Errorf("operation count disagrees with inventory")
	}
	for name, shape := range schemas {
		validateDocumentedShape(t, "schema "+name, shape, schemas)
	}
	for path, item := range paths {
		validateDocumentedShape(t, "path "+path, item, schemas)
	}
	t.Logf("validated %d API operations, %d paths, %d field schemas against native registrations", len(expected), len(paths), len(schemas))
}

func validateDocumentedShape(t *testing.T, label string, value any, schemas map[string]any) {
	t.Helper()
	switch node := value.(type) {
	case map[string]any:
		if ref, ok := node["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, "#/components/schemas/")
			if ref == name || schemas[name] == nil {
				t.Errorf("dangling schema reference %s: %s", label, ref)
			}
		}
		if node["type"] == "array" && node["items"] == nil {
			t.Errorf("array missing items: %s", label)
		}
		if node["type"] == "object" && node["x-native-flexible-json"] != true {
			props, present := node["properties"].(map[string]any)
			_, dictionary := node["additionalProperties"].(map[string]any)
			if !present && !dictionary && !(node["nullable"] == true && node["enum"] != nil) {
				t.Errorf("untyped object schema: %s", label)
			}
			if names, ok := node["required"].([]string); ok {
				for _, name := range names {
					if props[name] == nil {
						t.Errorf("required nonexistent field %s.%s", label, name)
					}
				}
			}
		}
		for key, child := range node {
			validateDocumentedShape(t, label+"."+key, child, schemas)
		}
	case []any:
		for _, child := range node {
			validateDocumentedShape(t, label, child, schemas)
		}
	}
}

func routeString(expr ast.Expr, env map[string]string) (string, bool) {
	switch x := expr.(type) {
	case *ast.BasicLit:
		v, e := strconv.Unquote(x.Value)
		return v, e == nil
	case *ast.Ident:
		v, ok := env[x.Name]
		return v, ok
	case *ast.BinaryExpr:
		left, a := routeString(x.X, env)
		right, b := routeString(x.Y, env)
		return left + right, a && b && x.Op == token.ADD
	case *ast.CallExpr:
		if id, ok := x.Fun.(*ast.Ident); ok && (id.Name == "exactRoute" || id.Name == "exact") && len(x.Args) == 1 {
			return routeString(x.Args[0], env)
		}
	}
	return "", false
}

func walkRouteStatements(t *testing.T, list []ast.Stmt, env map[string]string, functions map[string]*ast.FuncDecl, out map[string]bool) {
	t.Helper()
	copyEnv := func() map[string]string {
		c := map[string]string{}
		for k, v := range env {
			c[k] = v
		}
		return c
	}
	for _, statement := range list {
		switch stmt := statement.(type) {
		case *ast.AssignStmt:
			for i, lhs := range stmt.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && i < len(stmt.Rhs) {
					if value, valid := routeString(stmt.Rhs[i], env); valid {
						env[id.Name] = value
					}
				}
			}
		case *ast.RangeStmt:
			lit, ok := stmt.X.(*ast.CompositeLit)
			if !ok {
				t.Fatalf("unsupported route range at %v", stmt.Pos())
			}
			for _, element := range lit.Elts {
				child := copyEnv()
				if kv, keyed := element.(*ast.KeyValueExpr); keyed {
					if key, valid := routeString(kv.Key, env); valid {
						if id, yes := stmt.Key.(*ast.Ident); yes {
							child[id.Name] = key
						}
					}
				} else if value, valid := routeString(element, env); valid {
					if id, yes := stmt.Value.(*ast.Ident); yes {
						child[id.Name] = value
					}
				} else {
					t.Fatalf("unsupported route list element at %v", element.Pos())
				}
				walkRouteStatements(t, stmt.Body.List, child, functions, out)
			}
		case *ast.ExprStmt:
			call, ok := stmt.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			name := ""
			index := 1
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				name = fun.Name
			case *ast.SelectorExpr:
				name = fun.Sel.Name
				if name == "HandleFunc" || name == "Handle" {
					index = 0
				}
			}
			if name == "registerSavedSearches" {
				continue
			}
			if name != "HandleFunc" && name != "Handle" && name != "registerRoute" && name != "registerExact" {
				continue
			}
			if index >= len(call.Args) {
				t.Fatal("route without pattern")
			}
			pattern, valid := routeString(call.Args[index], env)
			if !valid {
				t.Fatalf("cannot evaluate route pattern at %v", call.Pos())
			}
			pattern = strings.TrimSuffix(pattern, "{$}")
			parts := strings.SplitN(pattern, " ", 2)
			if len(parts) == 2 && strings.HasPrefix(parts[1], "/api/") {
				out[pattern] = true
			}
		case *ast.IfStmt:
			walkRouteStatements(t, stmt.Body.List, copyEnv(), functions, out)
			if other, ok := stmt.Else.(*ast.BlockStmt); ok {
				walkRouteStatements(t, other.List, copyEnv(), functions, out)
			}
		}
	}
}

func schemaProperties(t *testing.T, shape map[string]any, schemas map[string]any) map[string]any {
	t.Helper()
	if choices, ok := shape["oneOf"].([]any); ok && len(choices) > 0 {
		shape = choices[0].(map[string]any)
	}
	if ref, ok := shape["$ref"].(string); ok {
		shape = schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
	}
	props, ok := shape["properties"].(map[string]any)
	if !ok {
		t.Fatal("missing object properties")
	}
	return props
}

func TestNativeOpenAPIAnonymousRequestDTOs(t *testing.T) {
	schemas := map[string]any{}
	privateSchemas(schemas)
	// Each binding is a decoded anonymous request struct in the real handler.
	// Fields are compared exactly, including nested settings objects and arrays.
	bindings := []struct{ pkg, function, method, path string }{
		{"accounts", "AvatarStyle", "POST", "/api/accounts/me/avatar-style/"},
		{"accounts", "Settings", "PUT", "/api/accounts/me/settings/"},
		{"accounts", "WardDetail", "PATCH", "/api/accounts/wards/{public_id}/"},
		{"accounts", "GuardianLinks", "POST", "/api/accounts/guardian-links/"},
		{"accounts", "AgeVerify", "POST", "/api/accounts/verify-age/"},
		{"accounts", "ObtainToken", "POST", "/api/auth/token/"},
		{"messaging", "ownKey", "POST", "/api/messaging/keys/"},
		{"messaging", "verify", "POST", "/api/messaging/verify/"},
		{"messaging", "conversations", "POST", "/api/messaging/conversations/"},
		{"messaging", "participantsHTTP", "POST", "/api/messaging/conversations/{id}/participants/"},
		{"messaging", "disappearingHTTP", "POST", "/api/messaging/conversations/{id}/disappearing/"},
		{"messaging", "reportHTTP", "POST", "/api/messaging/conversations/{id}/messages/{message_id}/report/"},
		{"recommendations", "interests", "PUT", "/api/recommendations/interests/"},
		{"recommendations", "topics", "PUT", "/api/recommendations/topics/"},
		{"safety", "ReportHTTP", "POST", "/api/safety/reports/"},
		{"safety", "BlockHTTP", "POST", "/api/safety/blocks/"},
		{"safety", "AppealHTTP", "POST", "/api/safety/appeals/"},
		{"safety", "ResolveAppealHTTP", "POST", "/api/safety/moderation/appeals/{id}/resolve/"},
		{"safety", "ResolveConcernHTTP", "POST", "/api/safety/moderation/concerns/{id}/resolve/"},
	}
	for _, binding := range bindings {
		t.Run(binding.pkg+"."+binding.function, func(t *testing.T) {
			packages, err := parser.ParseDir(token.NewFileSet(), filepath.Join("..", binding.pkg), func(info os.FileInfo) bool { return !strings.HasSuffix(info.Name(), "_test.go") }, 0)
			if err != nil {
				t.Fatal(err)
			}
			var body *ast.StructType
			for _, pkg := range packages {
				for _, file := range pkg.Files {
					for _, decl := range file.Decls {
						if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == binding.function {
							ast.Inspect(f.Body, func(n ast.Node) bool {
								if v, ok := n.(*ast.ValueSpec); ok {
									for _, name := range v.Names {
										if name.Name == "body" || name.Name == "in" {
											if shape, yes := v.Type.(*ast.StructType); yes {
												body = shape
											}
										}
									}
								}
								return true
							})
						}
					}
				}
			}
			if body == nil {
				t.Fatal("decoded request struct not found")
			}
			contract, valid := privateContract(binding.method, binding.path)
			if !valid || contract.request == nil {
				t.Fatal("missing request contract")
			}
			compareStructProperties(t, body, schemaProperties(t, contract.request, schemas), schemas)
		})
	}
}

func compareStructProperties(t *testing.T, body *ast.StructType, props map[string]any, schemas map[string]any) {
	t.Helper()
	expected, actual := []string{}, []string{}
	for _, field := range body.Fields.List {
		if field.Tag == nil {
			t.Fatal("untagged request field")
		}
		tag, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
		expected = append(expected, name)
		shape, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		kind := field.Type
		if ptr, ok := kind.(*ast.StarExpr); ok {
			kind = ptr.X
		}
		if nested, ok := kind.(*ast.StructType); ok {
			compareStructProperties(t, nested, schemaProperties(t, shape, schemas), schemas)
		}
		if scalar, ok := kind.(*ast.Ident); ok {
			want := map[string]string{"string": "string", "bool": "boolean", "int": "integer", "int64": "integer", "float64": "number"}[scalar.Name]
			if want != "" && shape["type"] != want {
				t.Errorf("field %s type got %v want %s", name, shape["type"], want)
			}
		}
		if _, ok := kind.(*ast.ArrayType); ok && shape["type"] != "array" {
			t.Errorf("field %s must be an array", name)
		}
	}
	for name := range props {
		actual = append(actual, name)
	}
	sort.Strings(expected)
	sort.Strings(actual)
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("request fields differ: DTO=%v schema=%v", expected, actual)
	}
}
