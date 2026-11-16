package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func writePackageJSON(t *testing.T, content string) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "package.json")
	if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
		t.Fatalf("unable to write test package.json: %s", err)
	}
	return filename
}

func TestReadPackagesFromFileMalformedJSONFailsClosed(t *testing.T) {
	lookup := NewNPMLookup(false).(*NPMLookup)
	err := lookup.ReadPackagesFromFile(writePackageJSON(t, `{"dependencies": {"foo": "1.0.0",}`))
	if err == nil {
		t.Fatal("expected an error for a malformed package.json, got nil")
	}
}

func TestReadPackagesFromFileMissingFile(t *testing.T) {
	lookup := NewNPMLookup(false).(*NPMLookup)
	if err := lookup.ReadPackagesFromFile(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected an error for a missing file, got nil")
	}
}

func TestReadPackagesFromFileRejectsInvalidDependencies(t *testing.T) {
	cases := map[string]string{
		"invalid package name":     `{"dependencies": {"Bad Name": "1.0.0"}}`,
		"uppercase package name":   `{"dependencies": {"Foo": "1.0.0"}}`,
		"empty version spec":       `{"dependencies": {"foo": ""}}`,
		"empty npm alias":          `{"dependencies": {"foo": "npm:"}}`,
		"npm alias with bad name":  `{"dependencies": {"foo": "npm:Bad Name@1.0.0"}}`,
		"npm alias with bad range": `{"dependencies": {"foo": "npm:bar@???"}}`,
		"unknown version spec":     `{"dependencies": {"foo": "???"}}`,
		"unknown protocol":         `{"dependencies": {"foo": "ftp://example.com/foo.tgz"}}`,
		"invalid bundled name":     `{"bundledDependencies": ["not a name"]}`,
	}
	for name, manifest := range cases {
		t.Run(name, func(t *testing.T) {
			lookup := NewNPMLookup(false).(*NPMLookup)
			if err := lookup.ReadPackagesFromFile(writePackageJSON(t, manifest)); err == nil {
				t.Fatalf("expected an error for manifest %s, got nil", manifest)
			}
		})
	}
}

func TestReadPackagesFromFileValidManifest(t *testing.T) {
	manifest := `{
		"dependencies": {"left-pad": "^1.3.0", "@scope/pkg": "latest"},
		"devDependencies": {"local-thing": "workspace:*"},
		"peerDependencies": {"aliased": "npm:real-pkg@1.0.0"},
		"optionalDependencies": {"tarball": "https://example.com/t.tgz"},
		"bundledDependencies": ["left-pad"]
	}`
	lookup := NewNPMLookup(false).(*NPMLookup)
	if err := lookup.ReadPackagesFromFile(writePackageJSON(t, manifest)); err != nil {
		t.Fatalf("expected no error, got: %s", err)
	}
	if len(lookup.Packages) != 6 {
		t.Fatalf("expected 6 packages, got %d", len(lookup.Packages))
	}
}

func TestClassifyVersionSpec(t *testing.T) {
	cases := []struct {
		spec        string
		kind        NPMReferenceKind
		aliasTarget string
	}{
		{"1.2.3", ReferenceVersion, ""},
		{"^1.2.3-beta.1", ReferenceVersion, ""},
		{">=1.0.0 <2.0.0", ReferenceVersion, ""},
		{"1.2.3 - 2.3.4", ReferenceVersion, ""},
		{"1.x || >=2.5.0", ReferenceVersion, ""},
		{"*", ReferenceVersion, ""},
		{"latest", ReferenceVersion, ""},
		{"beta-2", ReferenceVersion, ""},
		{"file:../local", ReferenceLocal, ""},
		{"link:../local", ReferenceLocal, ""},
		{"portal:../local", ReferenceLocal, ""},
		{"workspace:*", ReferenceLocal, ""},
		{"Workspace:^1.0.0", ReferenceLocal, ""},
		{"http://example.com/pkg.tgz", ReferenceURL, ""},
		{"https://example.com/pkg.tgz", ReferenceURL, ""},
		{"git+ssh://git@example.com/org/repo.git", ReferenceGit, ""},
		{"git+https://example.com/org/repo.git", ReferenceGit, ""},
		{"git://example.com/org/repo.git", ReferenceGit, ""},
		{"git@example.com:org/repo.git", ReferenceGit, ""},
		{"github:org/repo", ReferenceGit, ""},
		{"gitlab:org/repo", ReferenceGit, ""},
		{"bitbucket:org/repo", ReferenceGit, ""},
		{"org/repo", ReferenceGitHub, ""},
		{"org/repo#v1.0.0", ReferenceGitHub, ""},
		{"npm:real-pkg@1.0.0", ReferenceAlias, "real-pkg"},
		{"npm:@scope/real-pkg@^2.0.0", ReferenceAlias, "@scope/real-pkg"},
		{"npm:real-pkg", ReferenceAlias, "real-pkg"},
	}
	for _, c := range cases {
		t.Run(c.spec, func(t *testing.T) {
			kind, target, err := classifyVersionSpec(c.spec)
			if err != nil {
				t.Fatalf("unexpected error for %q: %s", c.spec, err)
			}
			if kind != c.kind || target != c.aliasTarget {
				t.Fatalf("classifyVersionSpec(%q) = (%d, %q), want (%d, %q)", c.spec, kind, target, c.kind, c.aliasTarget)
			}
		})
	}
}

func TestClassifyVersionSpecFailsClosed(t *testing.T) {
	for _, spec := range []string{"", "???", "npm:", "ftp://x/y", "a/b/c", "1.2.3 -", "@scope"} {
		if _, _, err := classifyVersionSpec(spec); err == nil {
			t.Fatalf("expected an error for spec %q, got nil", spec)
		}
	}
}

func TestValidNpmPackageName(t *testing.T) {
	valid := []string{"left-pad", "@scope/pkg", "foo.bar", "foo_bar", "a", "pkg~name"}
	invalid := []string{"", "Foo", "foo bar", "@scope", "@scope/", "@/pkg", ".hidden", "_under", "foo/bar", "foo@bar"}
	for _, name := range valid {
		if !validNpmPackageName(name) {
			t.Fatalf("expected %q to be a valid npm package name", name)
		}
	}
	for _, name := range invalid {
		if validNpmPackageName(name) {
			t.Fatalf("expected %q to be an invalid npm package name", name)
		}
	}
}

// fakeRegistry is an httptest server standing in for registry.npmjs.org.
type fakeRegistry struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	fake := &fakeRegistry{}
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.requests = append(fake.requests, r.URL.Path)
		fake.mu.Unlock()
		switch r.URL.Path {
		case "/left-pad/", "/@scope/pkg/":
			fmt.Fprintf(w, `{"name": "left-pad", "time": {}}`)
		case "/unpublished-pkg/":
			fmt.Fprintf(w, `{"name": "unpublished-pkg", "time": {"unpublished": {"name": "unpublished-pkg"}}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fake.Server.Close)
	return fake
}

func (f *fakeRegistry) requestedPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.requests...)
}

func TestPackagesNotInPublicWithFakeRegistry(t *testing.T) {
	registry := newFakeRegistry(t)
	manifest := `{
		"dependencies": {
			"left-pad": "^1.3.0",
			"missing-pkg": "1.0.0",
			"unpublished-pkg": "2.0.0",
			"@scope/pkg": "latest",
			"aliased-ok": "npm:left-pad@1.0.0",
			"aliased-missing": "npm:missing-target@1.0.0",
			"workspace-dep": "workspace:*",
			"file-dep": "file:../file-dep",
			"link-dep": "link:../link-dep",
			"url-dep": "https://example.com/url-dep.tgz",
			"git-dep": "git+ssh://git@example.com/org/git-dep.git",
			"github-shortcut": "github:org/repo"
		}
	}`
	lookup := NewNPMLookup(false).(*NPMLookup)
	lookup.RegistryURL = registry.URL
	if err := lookup.ReadPackagesFromFile(writePackageJSON(t, manifest)); err != nil {
		t.Fatalf("unexpected error reading manifest: %s", err)
	}

	got := lookup.PackagesNotInPublic()
	sort.Strings(got)
	want := []string{"aliased-missing", "missing-pkg", "unpublished-pkg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PackagesNotInPublic() = %v, want %v", got, want)
	}

	// The npm: alias must be resolved against its target package name, and
	// local/URL/git references must never be queried from the registry.
	requested := registry.requestedPaths()
	sort.Strings(requested)
	wantRequests := []string{"/@scope/pkg/", "/left-pad/", "/left-pad/", "/missing-pkg/", "/missing-target/", "/unpublished-pkg/"}
	if !reflect.DeepEqual(requested, wantRequests) {
		t.Fatalf("registry requests = %v, want %v", requested, wantRequests)
	}
}

func TestPackagesNotInPublicDefaultsToNpmRegistry(t *testing.T) {
	lookup := NewNPMLookup(false).(*NPMLookup)
	if lookup.registryURL() != defaultNpmRegistryURL {
		t.Fatalf("expected default registry %q, got %q", defaultNpmRegistryURL, lookup.registryURL())
	}
	lookup.RegistryURL = "https://registry.example.com/"
	if lookup.registryURL() != "https://registry.example.com" {
		t.Fatalf("expected trailing slash to be trimmed, got %q", lookup.registryURL())
	}
}
