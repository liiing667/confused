package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const defaultNpmRegistryURL = "https://registry.npmjs.org"

// PackageJSON represents the dependencies of an npm package
type PackageJSON struct {
	Dependencies         map[string]string `json:"dependencies,omitempty"`
	DevDependencies      map[string]string `json:"devDependencies,omitempty"`
	PeerDependencies     map[string]string `json:"peerDependencies,omitempty"`
	BundledDependencies  []string          `json:"bundledDependencies,omitempty"`
	BundleDependencies   []string          `json:"bundleDependencies,omitempty"`
	OptionalDependencies map[string]string `json:"optionalDependencies,omitempty"`
}

type NpmResponse struct {
	ID   string `json:"_id"`
	Name string `json:"name"`
	Time struct {
		Unpublished NpmResponseUnpublished `json:"unpublished"`
	} `json:"time"`
}

type NpmResponseUnpublished struct {
	Maintainers []struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	} `json:"maintainers"`
	Name string `json:"name"`
	Tags struct {
		Latest string `json:"latest"`
	} `json:"tags"`
	Time     time.Time `json:"time"`
	Versions []string  `json:"versions"`
}

// NotAvailable returns true if the package has its all versions unpublished making it susceptible for takeover
func (n *NpmResponse) NotAvailable() bool {
	// Check if a known field has a value
	return len(n.Time.Unpublished.Name) > 0
}

// NPMReferenceKind describes what an npm dependency version specification refers to.
type NPMReferenceKind int

const (
	// ReferenceVersion is a semver range or dist-tag resolved from the npm registry.
	ReferenceVersion NPMReferenceKind = iota
	// ReferenceLocal is a local filesystem or workspace reference (file:, link:, portal:, workspace:).
	ReferenceLocal
	// ReferenceURL is a direct tarball URL (http:, https:).
	ReferenceURL
	// ReferenceGit is a remote git repository or git hosting shortcut reference.
	ReferenceGit
	// ReferenceGitHub is a GitHub shorthand reference (org/repo[#ref]).
	ReferenceGitHub
	// ReferenceAlias is an npm: alias to another package in the registry.
	ReferenceAlias
)

// NPMLookup represents a collection of npm packages to be tested for dependency confusion.
type NPMLookup struct {
	Packages    []NPMPackage
	Verbose     bool
	RegistryURL string
}

type NPMPackage struct {
	Name        string
	Version     string
	Kind        NPMReferenceKind
	AliasTarget string
}

// NewNPMLookup constructs an `NPMLookup` struct and returns it.
func NewNPMLookup(verbose bool) PackageResolver {
	return &NPMLookup{Packages: []NPMPackage{}, Verbose: verbose, RegistryURL: defaultNpmRegistryURL}
}

// ReadPackagesFromFile reads package information from an npm package.json file
//
// Parsing and validation fail closed: a malformed manifest or an
// unrecognized dependency reference is reported as an error instead of
// being silently skipped.
func (n *NPMLookup) ReadPackagesFromFile(filename string) error {
	rawfile, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}
	data := PackageJSON{}
	if err := json.Unmarshal(rawfile, &data); err != nil {
		return fmt.Errorf("unable to parse %s: %w", filename, err)
	}
	dependencySets := []map[string]string{
		data.Dependencies,
		data.DevDependencies,
		data.PeerDependencies,
		data.OptionalDependencies,
	}
	for _, deps := range dependencySets {
		for pkgname, pkgversion := range deps {
			pkg, err := newNPMPackage(pkgname, pkgversion)
			if err != nil {
				return fmt.Errorf("invalid dependency %q in %s: %w", pkgname, filename, err)
			}
			n.Packages = append(n.Packages, pkg)
		}
	}
	for _, pkgname := range data.BundledDependencies {
		if !validNpmPackageName(pkgname) {
			return fmt.Errorf("invalid bundled dependency name %q in %s", pkgname, filename)
		}
		n.Packages = append(n.Packages, NPMPackage{Name: pkgname, Kind: ReferenceVersion})
	}
	for _, pkgname := range data.BundleDependencies {
		if !validNpmPackageName(pkgname) {
			return fmt.Errorf("invalid bundled dependency name %q in %s", pkgname, filename)
		}
		n.Packages = append(n.Packages, NPMPackage{Name: pkgname, Kind: ReferenceVersion})
	}
	return nil
}

// newNPMPackage validates a dependency name and version specification and
// classifies what the specification refers to.
func newNPMPackage(name, version string) (NPMPackage, error) {
	if !validNpmPackageName(name) {
		return NPMPackage{}, fmt.Errorf("invalid package name %q", name)
	}
	kind, aliasTarget, err := classifyVersionSpec(version)
	if err != nil {
		return NPMPackage{}, err
	}
	return NPMPackage{Name: name, Version: version, Kind: kind, AliasTarget: aliasTarget}, nil
}

var npmNameComponentRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._~-]*$`)

// validNpmPackageName reports whether name is a valid npm package name,
// optionally scoped (@scope/name).
func validNpmPackageName(name string) bool {
	if len(name) == 0 || len(name) > 214 {
		return false
	}
	if strings.HasPrefix(name, "@") {
		parts := strings.Split(name[1:], "/")
		if len(parts) != 2 {
			return false
		}
		return npmNameComponentRe.MatchString(parts[0]) && npmNameComponentRe.MatchString(parts[1])
	}
	if strings.ContainsAny(name, "/@") {
		return false
	}
	return npmNameComponentRe.MatchString(name)
}

var (
	npmLocalPrefixes   = []string{"file:", "link:", "portal:", "workspace:"}
	npmGitPrefixes     = []string{"git:", "git+ssh:", "git+http:", "git+https:", "git+file:", "ssh:", "git@"}
	npmHostingPrefixes = []string{"github:", "gitlab:", "bitbucket:", "gist:"}

	npmGitHubShorthandRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(#.+)?$`)
	npmSemverTokenRe     = regexp.MustCompile(`^(\^|~|>=|<=|>|<|=)?[vV]?(\d+|[xX*])(\.(\d+|[xX*])){0,2}(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	npmDistTagRe         = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)
)

// classifyVersionSpec determines what an npm dependency version specification
// refers to. Unknown or malformed specifications return an error so that
// callers fail closed instead of silently skipping the dependency.
//
// For ReferenceAlias the returned string is the aliased target package name.
func classifyVersionSpec(spec string) (NPMReferenceKind, string, error) {
	lower := strings.ToLower(spec)
	for _, prefix := range npmLocalPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return ReferenceLocal, "", nil
		}
	}
	if strings.HasPrefix(lower, "npm:") {
		target, err := parseNpmAlias(spec[len("npm:"):])
		if err != nil {
			return ReferenceVersion, "", err
		}
		return ReferenceAlias, target, nil
	}
	for _, prefix := range npmGitPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return ReferenceGit, "", nil
		}
	}
	for _, prefix := range npmHostingPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return ReferenceGit, "", nil
		}
	}
	if strings.HasPrefix(lower, "http:") || strings.HasPrefix(lower, "https:") {
		return ReferenceURL, "", nil
	}
	if npmGitHubShorthandRe.MatchString(spec) {
		return ReferenceGitHub, "", nil
	}
	if validVersionRange(spec) {
		return ReferenceVersion, "", nil
	}
	return ReferenceVersion, "", fmt.Errorf("unrecognized version specification %q", spec)
}

// parseNpmAlias parses the target of an npm: alias specification
// (<name>[@<range>]) and returns the validated target package name.
func parseNpmAlias(target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("empty npm: alias target")
	}
	name := target
	if idx := strings.LastIndex(target, "@"); idx > 0 {
		name = target[:idx]
		if versionRange := target[idx+1:]; !validVersionRange(versionRange) {
			return "", fmt.Errorf("invalid version range %q in npm: alias", versionRange)
		}
	}
	if !validNpmPackageName(name) {
		return "", fmt.Errorf("invalid package name %q in npm: alias", name)
	}
	return name, nil
}

// validVersionRange reports whether spec is a plausible npm semver range or
// dist-tag (e.g. ^1.2.3, >=1.0.0 <2.0.0, 1.2.3 - 2.3.4, *, latest).
func validVersionRange(spec string) bool {
	if spec == "" {
		return false
	}
	for _, alternative := range strings.Split(spec, "||") {
		if !validComparatorSet(strings.TrimSpace(alternative)) {
			return false
		}
	}
	return true
}

func validComparatorSet(set string) bool {
	fields := strings.Fields(set)
	if len(fields) == 0 {
		return false
	}
	if len(fields) == 1 && npmDistTagRe.MatchString(fields[0]) && !npmSemverTokenRe.MatchString(fields[0]) {
		return true
	}
	for i, token := range fields {
		if token == "-" {
			if i == 0 || i == len(fields)-1 {
				return false
			}
			continue
		}
		if !npmSemverTokenRe.MatchString(token) {
			return false
		}
	}
	return true
}

// PackagesNotInPublic determines if an npm package does not exist in the public npm package repository.
//
// Returns a slice of strings with any npm packages not in the public npm package repository
func (n *NPMLookup) PackagesNotInPublic() []string {
	notavail := []string{}
	for _, pkg := range n.Packages {
		switch pkg.Kind {
		case ReferenceLocal, ReferenceURL, ReferenceGit:
			continue
		case ReferenceGitHub:
			if !n.gitHubOrgExists(pkg.Version) {
				notavail = append(notavail, pkg.Name)
			}
			continue
		case ReferenceAlias:
			// An npm: alias fetches the target package from the registry, so the
			// target name is what needs to be checked for dependency confusion.
			if !n.isAvailableInPublic(pkg.AliasTarget, 0) {
				notavail = append(notavail, pkg.Name)
			}
			continue
		}
		if !n.isAvailableInPublic(pkg.Name, 0) {
			notavail = append(notavail, pkg.Name)
		}
	}
	return notavail
}

func (n *NPMLookup) registryURL() string {
	if n.RegistryURL != "" {
		return strings.TrimRight(n.RegistryURL, "/")
	}
	return defaultNpmRegistryURL
}

// isAvailableInPublic determines if an npm package exists in the public npm package repository.
//
// Returns true if the package exists in the public npm package repository.
func (n *NPMLookup) isAvailableInPublic(pkgname string, retry int) bool {
	if retry > 3 {
		fmt.Printf(" [W] Maximum number of retries exhausted for package: %s\n", pkgname)
		return false
	}
	registry := n.registryURL()
	if n.Verbose {
		fmt.Printf("Checking: %s/%s/ : ", registry, pkgname)
	}
	resp, err := http.Get(registry + "/" + pkgname + "/")
	if err != nil {
		fmt.Printf(" [W] Error when trying to request %s/%s/ : %s\n", registry, pkgname, err)
		return false
	}
	defer resp.Body.Close()
	if n.Verbose {
		fmt.Printf("%s\n", resp.Status)
	}
	if resp.StatusCode == http.StatusOK {
		npmResp := NpmResponse{}
		body, _ := ioutil.ReadAll(resp.Body)
		_ = json.Unmarshal(body, &npmResp)
		if npmResp.NotAvailable() {
			if n.Verbose {
				fmt.Printf("[W] Package %s was found, but all its versions are unpublished, making anyone able to takeover the namespace.\n", pkgname)
			}
			return false
		}
		return true
	} else if resp.StatusCode == 429 {
		fmt.Printf(" [!] Server responded with 429 (Too many requests), throttling and retrying...\n")
		time.Sleep(10 * time.Second)
		retry = retry + 1
		return n.isAvailableInPublic(pkgname, retry)
	}
	return false
}

// gitHubOrgExists returns true if GitHub organization / user exists
func (n NPMLookup) gitHubOrgExists(pkgversion string) bool {
	orgName := strings.Split(pkgversion, "/")[0]
	if len(orgName) > 0 {
		if n.Verbose {
			fmt.Print("Checking: https://github.com/" + orgName + " : ")
		}
		resp, err := http.Get("https://github.com/" + orgName)
		if err != nil {
			fmt.Printf(" [W] Error while trying to request https://github.com/"+orgName+" : %s\n", err)
			return false
		}
		defer resp.Body.Close()
		if n.Verbose {
			fmt.Printf("%d\n", resp.StatusCode)
		}
		return resp.StatusCode == 200
	}
	return false
}
