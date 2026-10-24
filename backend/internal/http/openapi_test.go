package httpapi

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestOpenAPIMatchesRouter keeps docs/openapi.yaml in sync with the routes
// registered in router.go: every route must be documented and every
// documented operation must exist.
func TestOpenAPIMatchesRouter(t *testing.T) {
	src, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	var routes []string
	for _, m := range regexp.MustCompile(`mux\.Handle(?:Func)?\("([A-Z]+) (/[^"]*)"`).FindAllStringSubmatch(string(src), -1) {
		routes = append(routes, m[1]+" "+m[2])
	}
	if len(routes) < 10 {
		t.Fatalf("found only %d routes in router.go; did the registration style change?", len(routes))
	}

	raw, err := os.ReadFile("../../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	var documented []string
	for path, item := range spec.Paths {
		for key := range item {
			switch key {
			case "get", "post", "put", "patch", "delete":
				documented = append(documented, strings.ToUpper(key)+" "+path)
			}
		}
	}

	slices.Sort(routes)
	slices.Sort(documented)
	for _, r := range routes {
		if !slices.Contains(documented, r) {
			t.Errorf("route %q is not documented in docs/openapi.yaml", r)
		}
	}
	for _, d := range documented {
		if !slices.Contains(routes, d) {
			t.Errorf("docs/openapi.yaml documents %q, which router.go does not register", d)
		}
	}
}
