package api_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/api"
	"gopkg.in/yaml.v3"
)

// pathParamRe matches OpenAPI / Go ServeMux path parameters like {id}.
var pathParamRe = regexp.MustCompile(`\{[^/}]+\}`)

// normalizeRouteKey turns "METHOD /api/v1/foo/{id}" into a comparable key
// with path params collapsed to "{}" so {id} and {userId} compare equal.
func normalizeRouteKey(method, path string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	path = pathParamRe.ReplaceAllString(path, "{}")
	return method + " " + path
}

func parseRoutePattern(pattern string) (method, path string, ok bool) {
	pattern = strings.TrimSpace(pattern)
	method, path, found := strings.Cut(pattern, " ")
	if !found || method == "" || path == "" {
		return "", "", false
	}
	if !strings.HasPrefix(path, "/api/v1") {
		return "", "", false
	}
	return method, path, true
}

func findOpenAPIPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// server/internal/api/openapi_test.go → repo root → docs/api/openapi.yaml
	dir := filepath.Dir(file)
	candidates := []string{
		filepath.Join(dir, "..", "..", "..", "docs", "api", "openapi.yaml"),
		filepath.Join("docs", "api", "openapi.yaml"),
		filepath.Join("..", "docs", "api", "openapi.yaml"),
		filepath.Join("..", "..", "docs", "api", "openapi.yaml"),
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			abs, err := filepath.Abs(c)
			if err != nil {
				return c
			}
			return abs
		}
	}
	t.Fatalf("docs/api/openapi.yaml not found (tried from %s)", dir)
	return ""
}

// openAPIDoc is the minimal YAML shape needed for path+method coverage.
type openAPIDoc struct {
	Paths map[string]map[string]any `yaml:"paths"`
}

// httpMethods listed in OpenAPI path items we care about.
var openAPIMethods = []string{
	"get", "post", "put", "patch", "delete", "head", "options", "trace",
}

func loadOpenAPIRoutes(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	var doc openAPIDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse openapi yaml: %v", err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("openapi paths empty")
	}

	// key → original "METHOD path" for error messages
	out := make(map[string]string)
	for p, item := range doc.Paths {
		if !strings.HasPrefix(p, "/api/v1") {
			continue
		}
		for _, m := range openAPIMethods {
			if _, has := item[m]; !has {
				continue
			}
			orig := strings.ToUpper(m) + " " + p
			key := normalizeRouteKey(m, p)
			if prev, dup := out[key]; dup {
				t.Fatalf("duplicate openapi route keys after normalize: %q and %q", prev, orig)
			}
			out[key] = orig
		}
	}
	return out
}

func TestOpenAPICoversRoutes(t *testing.T) {
	specPath := findOpenAPIPath(t)
	specRoutes := loadOpenAPIRoutes(t, specPath)

	codeRoutes := make(map[string]string) // key → original pattern
	for _, pattern := range api.Routes() {
		method, path, ok := parseRoutePattern(pattern)
		if !ok {
			t.Fatalf("Routes() entry not METHOD /api/v1/... : %q", pattern)
		}
		key := normalizeRouteKey(method, path)
		if prev, dup := codeRoutes[key]; dup {
			t.Fatalf("duplicate code route keys after normalize: %q and %q", prev, pattern)
		}
		codeRoutes[key] = pattern
	}
	if len(codeRoutes) == 0 {
		t.Fatal("api.Routes() returned no /api/v1 routes")
	}

	var missingInSpec []string
	for key, pattern := range codeRoutes {
		if _, ok := specRoutes[key]; !ok {
			missingInSpec = append(missingInSpec, pattern)
		}
	}
	var missingInCode []string
	for key, orig := range specRoutes {
		if _, ok := codeRoutes[key]; !ok {
			missingInCode = append(missingInCode, orig)
		}
	}

	if len(missingInSpec) > 0 || len(missingInCode) > 0 {
		sort.Strings(missingInSpec)
		sort.Strings(missingInCode)
		t.Errorf("OpenAPI ↔ route coverage mismatch (spec=%s)", specPath)
		for _, r := range missingInSpec {
			t.Errorf("  route registered but missing from openapi.yaml: %s", r)
		}
		for _, r := range missingInCode {
			t.Errorf("  openapi.yaml path+method with no registered route: %s", r)
		}
	}
}
