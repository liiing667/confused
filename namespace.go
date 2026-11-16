package main

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// pep503SeparatorRe matches runs of characters that PEP 503 treats as equivalent.
var pep503SeparatorRe = regexp.MustCompile(`[-_.]+`)

// normalizeNamespaceName canonicalizes a package name according to the
// ecosystem's own equivalence rules so that a safe namespace matches every
// spelling the ecosystem would consider identical.
func normalizeNamespaceName(ecosystem, name string) string {
	switch ecosystem {
	case "pip":
		// PEP 503: case-insensitive, runs of -, _ and . are equivalent.
		return pep503SeparatorRe.ReplaceAllString(strings.ToLower(name), "-")
	case "composer":
		// Packagist package names are case-insensitive.
		return strings.ToLower(name)
	default:
		// npm, mvn and rubygems names are case-sensitive.
		return name
	}
}

// matchNamespace matches a package name against a safe namespace pattern.
//
// Matching is segment-based on "/", the namespace separator used by npm
// scopes (@scope/name), Packagist vendors (vendor/package) and Maven
// coordinates (group/artifact). It relies on path.Match, which interprets
// "/" identically on every host platform, unlike filepath.Match which
// follows the host OS path separator.
func matchNamespace(pattern, name string) (bool, error) {
	patternParts := strings.Split(pattern, "/")
	nameParts := strings.Split(name, "/")
	if len(patternParts) != len(nameParts) {
		return false, nil
	}
	for i := range patternParts {
		ok, err := path.Match(patternParts[i], nameParts[i])
		if err != nil {
			return false, fmt.Errorf("invalid safe namespace pattern %q: %w", pattern, err)
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// removeSafe removes known-safe package names from the slice
func removeSafe(packages []string, safespaces string, ecosystem string) []string {
	retSlice := []string{}
	safeNamespaces := []string{}
	for _, s := range strings.Split(safespaces, ",") {
		if s = strings.TrimSpace(s); s != "" {
			safeNamespaces = append(safeNamespaces, normalizeNamespaceName(ecosystem, s))
		}
	}
	for _, p := range packages {
		ignored := false
		normalized := normalizeNamespaceName(ecosystem, p)
		for _, s := range safeNamespaces {
			ok, err := matchNamespace(s, normalized)
			if err != nil {
				fmt.Printf(" [W] %s\n", err)
				continue
			}
			if ok {
				ignored = true
				break
			}
		}
		if !ignored {
			retSlice = append(retSlice, p)
		}
	}
	return retSlice
}
