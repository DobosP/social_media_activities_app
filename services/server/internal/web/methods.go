package web

import (
	_ "embed"
	"encoding/json"
)

//go:embed methods.json
var methodsJSON []byte
var legacyMethods = func() map[string][]string {
	var methods map[string][]string
	_ = json.Unmarshal(methodsJSON, &methods)
	return methods
}()

func hasMethod(name, method string) bool {
	for _, value := range legacyMethods[name] {
		if value == method {
			return true
		}
	}
	return false
}
