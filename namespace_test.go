package main

import (
	"reflect"
	"testing"
)

func TestMatchNamespace(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"@myorg/*", "@myorg/pkg", true},
		{"@myorg/*", "@myorg/nested/pkg", false},
		{"@myorg/*", "@other/pkg", false},
		{"internal-*", "internal-utils", true},
		{"internal-?", "internal-a", true},
		{"internal-?", "internal-ab", false},
		{"*", "anything", true},
		{"*", "@scope/pkg", false},
		{"exact-name", "exact-name", true},
		{"exact-name", "other-name", false},
		{"com.example/*", "com.example/app", true},
		{"vendor/pkg", "vendor/pkg", true},
		{"vendor/*", "vendor/pkg", true},
	}
	for _, c := range cases {
		got, err := matchNamespace(c.pattern, c.name)
		if err != nil {
			t.Fatalf("matchNamespace(%q, %q) returned error: %s", c.pattern, c.name, err)
		}
		if got != c.want {
			t.Fatalf("matchNamespace(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestMatchNamespaceInvalidPattern(t *testing.T) {
	if _, err := matchNamespace("[unclosed", "name"); err == nil {
		t.Fatal("expected an error for an invalid pattern, got nil")
	}
}

func TestRemoveSafe(t *testing.T) {
	packages := []string{"@myorg/lib", "@myorg/nested/lib", "internal-utils", "public-pkg"}
	got := removeSafe(packages, "@myorg/*, internal-*", "npm")
	want := []string{"@myorg/nested/lib", "public-pkg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("removeSafe() = %v, want %v", got, want)
	}
}

func TestRemoveSafeEmptySafespaces(t *testing.T) {
	packages := []string{"a", "b"}
	if got := removeSafe(packages, "", "npm"); !reflect.DeepEqual(got, packages) {
		t.Fatalf("removeSafe() = %v, want %v", got, packages)
	}
}

func TestRemoveSafeInvalidPatternKeepsPackage(t *testing.T) {
	packages := []string{"foo"}
	if got := removeSafe(packages, "[unclosed", "npm"); !reflect.DeepEqual(got, packages) {
		t.Fatalf("removeSafe() = %v, want %v", got, packages)
	}
}

func TestRemoveSafeEcosystemNormalization(t *testing.T) {
	cases := []struct {
		ecosystem string
		packages  []string
		safe      string
		want      []string
	}{
		// PEP 503: case-insensitive, - _ . are equivalent.
		{"pip", []string{"My_Pkg", "other"}, "my-pkg", []string{"other"}},
		{"pip", []string{"my.pkg"}, "my_pkg", []string{}},
		// Packagist names are case-insensitive.
		{"composer", []string{"Vendor/Package"}, "vendor/package", []string{}},
		// npm names are case-sensitive.
		{"npm", []string{"MyPkg"}, "mypkg", []string{"MyPkg"}},
		// Maven coordinates are matched as group/artifact.
		{"mvn", []string{"com.example/app", "org.other/lib"}, "com.example/*", []string{"org.other/lib"}},
	}
	for _, c := range cases {
		if got := removeSafe(c.packages, c.safe, c.ecosystem); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("removeSafe(%v, %q, %q) = %v, want %v", c.packages, c.safe, c.ecosystem, got, c.want)
		}
	}
}
