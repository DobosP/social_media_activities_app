package web

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
)

type legacyRoute struct {
	Name     string
	Pattern  *regexp.Regexp
	Keys     []string
	Priority int
}

var typedConverter = regexp.MustCompile(`<([^:>]+):([^>]+)>`)

func buildLegacyRoutes() map[string][]legacyRoute {
	buckets := map[string][]legacyRoute{}
	for name, path := range routePaths {
		keys := []string{}
		expression := "^/"
		last := 0
		priority := len(path)
		for _, match := range typedConverter.FindAllStringSubmatchIndex(path, -1) {
			expression += regexp.QuoteMeta(path[last:match[0]])
			kind, key := path[match[2]:match[3]], path[match[4]:match[5]]
			keys = append(keys, key)
			switch kind {
			case "int":
				expression += `([0-9]+)`
			case "uuid":
				expression += `([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`
			case "slug":
				expression += `([-a-zA-Z0-9_]+)`
			default:
				expression += `([^/]+)`
			}
			priority -= match[1] - match[0] + 20
			last = match[1]
		}
		expression += regexp.QuoteMeta(path[last:]) + "$"
		prefix := strings.Split(path, "/")[0]
		if strings.Contains(prefix, "<") {
			prefix = ""
		}
		buckets[prefix] = append(buckets[prefix], legacyRoute{name, regexp.MustCompile(expression), keys, priority})
	}
	for prefix, routes := range buckets {
		sort.Slice(routes, func(i, j int) bool {
			if routes[i].Priority != routes[j].Priority {
				return routes[i].Priority > routes[j].Priority
			}
			return routes[i].Name < routes[j].Name
		})
		buckets[prefix] = routes
	}
	return buckets
}

var legacyRouteBuckets = buildLegacyRoutes()

func (s *Server) legacyHTTP(w http.ResponseWriter, r *http.Request) {
	prefix := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
	for _, route := range legacyRouteBuckets[prefix] {
		matches := route.Pattern.FindStringSubmatch(r.URL.Path)
		if matches == nil {
			continue
		}
		for i, key := range route.Keys {
			r.SetPathValue(key, matches[i+1])
		}
		if r.Method == "GET" || r.Method == "HEAD" {
			if hasMethod(route.Name, "GET") || len(legacyMethods[route.Name]) == 0 && !actionOnly[route.Name] {
				s.page(w, r, route.Name)
				return
			}
		}
		if r.Method == "POST" {
			if _, ok := actions[route.Name]; ok || hasMethod(route.Name, "POST") {
				s.action(w, r, route.Name)
				return
			}
		}
		w.Header().Set("Allow", strings.Join(legacyMethods[route.Name], ", "))
		http.Error(w, "Method not allowed", 405)
		return
	}
	http.NotFound(w, r)
}
