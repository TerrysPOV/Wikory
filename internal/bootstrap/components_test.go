package bootstrap

import (
	"slices"
	"testing"
)

func names(cs []Component) []string {
	var n []string
	for _, c := range cs {
		n = append(n, c.Prefix)
	}
	return n
}

func TestDetectComponentsByGoPackagePackageJSONAndTopLevelDirectory(t *testing.T) {
	paths := []string{
		"cmd/wikory/main.go",
		"internal/store/store.go", "internal/store/schema.go", "internal/store/testdata/x.json",
		"web/package.json", "web/src/app.ts",
		"scripts/exit-checks.sh",
		"main.go",
	}
	cs := DetectComponents(paths)
	want := []string{".", "cmd/wikory", "internal/store", "scripts", "web"}
	if !slices.Equal(names(cs), want) {
		t.Fatalf("components = %v, want %v", names(cs), want)
	}
	byPrefix := map[string]Component{}
	for _, c := range cs {
		byPrefix[c.Prefix] = c
	}
	if byPrefix["internal/store"].Files != 3 {
		t.Fatalf("testdata belongs to the nearest enclosing package: %+v", byPrefix["internal/store"])
	}
	if byPrefix["web"].Files != 2 {
		t.Fatalf("web: %+v", byPrefix["web"])
	}
	if !byPrefix["cmd/wikory"].Entry || !byPrefix["."].Entry {
		t.Fatal("a component holding a main.go is an entry point")
	}
	if byPrefix["internal/store"].Entry {
		t.Fatal("a library package is not an entry point")
	}
}

func TestDetectComponentsIsDeterministic(t *testing.T) {
	a := DetectComponents([]string{"b/x.go", "a/y.go", "a/z.go"})
	b := DetectComponents([]string{"a/z.go", "a/y.go", "b/x.go"})
	if !slices.Equal(names(a), names(b)) {
		t.Fatalf("%v vs %v", names(a), names(b))
	}
}
