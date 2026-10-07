package detector

import (
	"regexp"
	"strings"
)

var nonExecutableExtensions = []string{
	".vsix",       // VS Code extension
	".deb",        // Debian package
	".rpm",        // RPM package
	".nupkg",      // NuGet package
	".whl",        // Python wheel
	".msi",        // Windows installer
	".msix",       // Windows app package
	".msixbundle", // Windows app package bundle
	".appx",       // Windows app package
	".appxbundle", // Windows app package bundle
	".pkg",        // macOS installer package
	".dmg",        // macOS disk image
}

// IsNonExecutableFile checks if filename is a non-executable package format
func IsNonExecutableFile(filename string) bool {
	lowerName := strings.ToLower(filename)
	for _, ext := range nonExecutableExtensions {
		if strings.HasSuffix(lowerName, ext) {
			return true
		}
	}
	return false
}

// installerPattern names a setup program rather than the tool: a
// "<Product>_<version>_x64-setup.exe" is a PE file with an arch token, so it
// scores like a Windows build and passes --verify-extraction, and only its
// name says it installs a desktop app. The word must stand on its own
// between separators, so "uninstaller" and "setuptools" are tools.
var installerPattern = regexp.MustCompile(`(?i)(^|[-_. ])(setup|installer)([-_. ]|$)`)

// IsInstallerFile checks if filename names an installer of appName's project
// rather than a build of the tool. An app whose own name says "setup" or
// "installer" keeps its assets.
func IsInstallerFile(appName, filename string) bool {
	return installerPattern.MatchString(filename) && !installerPattern.MatchString(appName)
}

// nameMatches reports whether an asset's name carries the app's name as a
// run of whole tokens — case and the separator characters aside — so
// "harper-cli" is found in "harper-cli-x86_64-pc-windows-msvc.zip" and
// "golangci_lint-1.60.0" but not in "harper-ls-…", "harper-c-linux-…" or
// "Harper_2.11.0_x64-setup.exe", and "jq" not in "jquery". An empty app name
// matches nothing.
func nameMatches(appName, assetName string) bool {
	app := nameTokens(appName)
	return app != "-" && strings.Contains(nameTokens(assetName), app)
}

// nameTokens lowercases s, turns every separator into "-" and wraps the
// result in "-", so a token run can be found only at token boundaries.
func nameTokens(s string) string {
	return "-" + strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', '.', ' ':
			return '-'
		default:
			return r
		}
	}, strings.ToLower(s)) + "-"
}

// attestationExtensions name what a release publishes about an asset rather
// than the asset: signatures, certificates, provenance and SBOMs. They score
// like the archive they accompany, so without this list a "tool.tar.gz.proof"
// stands next in line when the archive itself cannot be verified.
var attestationExtensions = []string{
	".proof",
	".sig",
	".asc",
	".minisig",
	".pem",
	".crt",
	".cert",
	".sigstore",
	".sigstore.json",
	".intoto.jsonl",
	".sbom",
	".spdx.json",
	".cdx.json",
}

// IsAttestationFile checks if filename is a signature, certificate, provenance
// or SBOM file published beside an asset.
func IsAttestationFile(filename string) bool {
	lowerName := strings.ToLower(filename)
	for _, ext := range attestationExtensions {
		if strings.HasSuffix(lowerName, ext) {
			return true
		}
	}
	return false
}

var checksumExtensions = []string{
	".sha256",
	".sha256sum",
	".sha512",
	".sha512sum",
	".md5",
	".md5sum",
	".checksum",
	".checksums",
	".txt", // Often used for checksums
}

// IsChecksumFile checks if filename is a checksum file
func IsChecksumFile(filename string) bool {
	lowerName := strings.ToLower(filename)

	for _, ext := range checksumExtensions {
		if strings.HasSuffix(lowerName, ext) {
			return true
		}
	}

	if strings.Contains(lowerName, "checksum") || strings.Contains(lowerName, "hash") {
		return true
	}

	return false
}
