package api_test

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/api"
)

// readOpenAPI decodes docs/contracts/openapi/control-plane.v1.yaml into spec.
func readOpenAPI(t *testing.T, spec any) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "docs", "contracts", "openapi", "control-plane.v1.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal(err)
		}
		t.Skipf("OpenAPI document not found: %v", err)
	}
	if err := yaml.Unmarshal(raw, spec); err != nil {
		t.Fatal(err)
	}
}

// TestRoutesMatchOpenAPI keeps docs/contracts/openapi/control-plane.v1.yaml and the route tables
// identical, including which listener serves which path (ADR-016).
func TestRoutesMatchOpenAPI(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	readOpenAPI(t, &spec)
	var publicSpec, internalSpec []string
	for p, ops := range spec.Paths {
		for method := range ops {
			key := strings.ToUpper(method) + " " + p
			if strings.HasPrefix(p, "/api/") {
				publicSpec = append(publicSpec, key)
			} else {
				internalSpec = append(internalSpec, key)
			}
		}
	}
	keys := func(rs []api.Route) []string {
		out := make([]string, 0, len(rs))
		for _, r := range rs {
			out = append(out, r.Method+" "+r.Pattern)
		}
		return out
	}
	for name, pair := range map[string][2][]string{
		"public":   {publicSpec, keys(api.PublicRoutes())},
		"internal": {internalSpec, keys(api.InternalRoutes())},
	} {
		sort.Strings(pair[0])
		sort.Strings(pair[1])
		if strings.Join(pair[0], "\n") != strings.Join(pair[1], "\n") {
			t.Errorf("%s routes differ from OpenAPI\nspec:\n  %s\nimplemented:\n  %s", name, strings.Join(pair[0], "\n  "), strings.Join(pair[1], "\n  "))
		}
	}
}
