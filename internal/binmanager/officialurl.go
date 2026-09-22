package binmanager

import (
	"net/url"
	"slices"
	"strings"
)

// DeriveOfficialURL returns the page a reader would open to learn about an app,
// read off what the app already declares. It is a pure function of the
// declaration: no network, no guessing, and no partial answer — a shape this
// does not recognize yields "" rather than a link that might be wrong.
//
//	binary with a forge release URL → the repository page
//	node, bun with a package name   → the npm package page
//	uv with a package name          → the PyPI project page
//	go with a module path           → the pkg.go.dev page
//	jvm with a Maven Central JAR    → the Maven Central artifact page
//	jvm with a forge-hosted JAR     → the repository page
//	shell                           → none
func DeriveOfficialURL(app App) string {
	switch {
	case app.Binary != nil:
		return repositoryFromBinaries(app.Binary.Binaries)
	case app.Node != nil:
		return npmPage(app.Node.PackageName)
	case app.Bun != nil:
		return npmPage(app.Bun.PackageName)
	case app.Uv != nil:
		return pypiPage(app.Uv.PackageName)
	case app.Go != nil:
		return goPackagePage(app.Go.PackageName)
	case app.Jvm != nil:
		return jvmPage(app.Jvm.JarURL)
	default:
		return ""
	}
}

// forgeHosts are the hosts whose release-download paths name a repository
// unambiguously. A self-hosted forge is not on this list: the same path shape
// under an unknown host could be anything.
var forgeHosts = map[string]bool{
	"codeberg.org": true,
	"gitea.com":    true,
	"github.com":   true,
	"gitlab.com":   true,
}

// repositoryFromForgeURL turns a release-download URL into its repository page,
// or returns "" when the URL is not one.
func repositoryFromForgeURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || !forgeHosts[parsed.Host] {
		return ""
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 3 {
		return ""
	}
	// GitLab separates a project's own paths from its (possibly nested) namespace
	// with "/-/"; GitHub, Gitea and Codeberg all publish under
	// <owner>/<repo>/releases/download/<tag>/<file>.
	cut := -1
	for index, segment := range segments {
		if segment == "-" || (segment == "releases" && index >= 2) {
			cut = index
			break
		}
	}
	if cut < 2 {
		return ""
	}
	return "https://" + parsed.Host + "/" + strings.Join(segments[:cut], "/")
}

// repositoryFromBinaries derives one repository from every platform's download
// URL. Platforms that disagree yield nothing: an app whose macOS build comes
// from one repository and whose Linux build comes from another has no single
// home page, and picking either would be a guess.
func repositoryFromBinaries(binaries MapOfBinaries) string {
	found := ""
	for _, osName := range sortedKeys(binaries) {
		for _, arch := range sortedKeys(binaries[osName]) {
			for _, platform := range sortedKeys(binaries[osName][arch]) {
				repository := repositoryFromForgeURL(binaries[osName][arch][platform].URL)
				if repository == "" {
					continue
				}
				if found != "" && found != repository {
					return ""
				}
				found = repository
			}
		}
	}
	return found
}

// goPackagePage returns the pkg.go.dev page for a module or package path.
func goPackagePage(packagePath string) string {
	path := strings.TrimSpace(packagePath)
	if at := strings.Index(path, "@"); at > 0 {
		path = path[:at]
	}
	path = strings.Trim(path, "/")
	if path == "" || !strings.Contains(path, ".") || !strings.Contains(path, "/") {
		return ""
	}
	return "https://pkg.go.dev/" + path
}

// jvmPage returns the Maven Central artifact page for a JAR published there, or
// the repository page for a JAR published by a forge.
func jvmPage(jarURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(jarURL))
	if err != nil {
		return ""
	}
	if parsed.Host == "repo1.maven.org" || parsed.Host == "repo.maven.apache.org" {
		// maven2/<group/path>/<artifactId>/<version>/<file>.jar
		segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(segments) < 5 || segments[0] != "maven2" {
			return ""
		}
		coordinates := segments[1 : len(segments)-3]
		artifact := segments[len(segments)-3]
		if len(coordinates) == 0 || artifact == "" {
			return ""
		}
		return "https://central.sonatype.com/artifact/" + strings.Join(coordinates, ".") + "/" + artifact
	}
	return repositoryFromForgeURL(jarURL)
}

func npmPage(packageName string) string {
	name := strings.TrimSpace(packageName)
	if name == "" {
		return ""
	}
	return "https://www.npmjs.com/package/" + name
}

func pypiPage(packageName string) string {
	name := strings.TrimSpace(packageName)
	if name == "" {
		return ""
	}
	return "https://pypi.org/project/" + name + "/"
}

func sortedKeys[K ~string, V any](values map[K]V) []K {
	keys := make([]K, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
