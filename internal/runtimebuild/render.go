// Package runtimebuild renders a runtime definition (base image + system
// packages + a language manifest) into a Containerfile and a build context,
// per ARCHITECTURE.md §4.4 (task 4.3). It does not talk to Podman - that is
// internal/podman's BuildImage, given this package's output.
package runtimebuild

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/r3dpan/project-descendence/internal/podman"
	"github.com/r3dpan/project-descendence/internal/store"
)

// CuratedBaseImages are the platform defaults per language (task 4.2,
// decision #25: Debian across the board, so PowerShell and Python resolve
// against glibc and there is one base family rather than a per-language
// branch). A runtime may still name a different base image explicitly - these
// are what CreateRuntime falls back to when the caller does not.
var CuratedBaseImages = map[string]string{
	store.LangPython:     "docker.io/library/python:3.12-slim-bookworm",
	store.LangPowerShell: "mcr.microsoft.com/powershell:7.4-debian-12",
	store.LangNode:       "docker.io/library/node:20-bookworm-slim",
}

// Definition is the rendering input: exactly the columns a runtimes row
// carries plus nothing derived, so InputHash and Render agree on what "the
// inputs" are.
type Definition struct {
	BaseImage    string
	SysPackages  []string
	Lang         string
	LangManifest string // empty means "no language dependencies to install"
}

const manifestPath = "manifest"

// manifestDestPaths is where the COPYed-in manifest lands inside the build
// context, per language. Python's pip and a plain npm install accept any
// filename, but PSResourceGet's -RequiredResourceFile insists on a literal
// .psd1 or .json extension - found the hard way (build failure: "The
// RequiredResourceFile must have either a '.json' or '.psd1' extension")
// when testing this phase's exit check, since the generic "/tmp/manifest"
// name ARCHITECTURE.md §4.4 originally sketched only works for two of the
// three languages.
var manifestDestPaths = map[string]string{
	store.LangPython:     "/tmp/manifest",
	store.LangPowerShell: "/tmp/manifest.psd1",
	store.LangNode:       "/tmp/manifest",
}

// installStepTemplates renders the RUN line that installs a language's
// dependencies from the manifest COPYed in at manifestDestPaths[lang].
//
// npm has no lock file here (only package.json content is stored), so this
// is `npm install`, not `npm ci` - slower and less reproducible than a real
// Node build pipeline, but this platform never asked for one; Node was not
// part of Phase 4's exit check (Python + PowerShell) and this path is
// correspondingly less exercised.
var installStepTemplates = map[string]string{
	store.LangPython:     "pip install --no-cache-dir -r %s",
	store.LangPowerShell: `pwsh -NoLogo -NonInteractive -Command "Install-PSResource -RequiredResourceFile %s -TrustRepository -Scope AllUsers"`,
	store.LangNode:       "mkdir -p /tmp/npm-install && cp %s /tmp/npm-install/package.json && npm install --omit=dev --prefix /tmp/npm-install && cp -r /tmp/npm-install/node_modules /usr/local/lib/node_modules",
}

const containerfileTemplate = `FROM {{.BaseImage}}
{{- if .DisableDotnetIPv6}}
# .NET's HttpClient (what PSResourceGet/PowerShellGet use to reach PSGallery)
# falls back from IPv6 to IPv4 far more slowly than curl's Happy Eyeballs
# does. On a network where IPv6 is routed but blackholed rather than
# rejected outright - measured here, not assumed: a plain HTTPS HEAD request
# went from hanging past 100s to 0.8s with this one variable set - every
# .NET-based install effectively never completes. Harmless where IPv6 works.
ENV DOTNET_SYSTEM_NET_DISABLEIPV6=1
{{- end}}
{{- if .SysPackages}}
RUN apt-get update && apt-get install -y --no-install-recommends {{.SysPackages}} && rm -rf /var/lib/apt/lists/*
{{- end}}
{{- if .LangManifest}}
COPY manifest {{.ManifestDest}}
RUN {{.InstallStep}}
{{- end}}
`

// Render produces the Containerfile text for def. Layer order matches
// §4.4: system packages before language packages, language packages before
// anything that changes more often - here there is nothing after, since a
// runtime image is exactly base + sys packages + language packages.
func Render(def Definition) (string, error) {
	if def.BaseImage == "" {
		return "", fmt.Errorf("runtimebuild: base image is empty")
	}
	installStep := ""
	manifestDest := ""
	if def.LangManifest != "" {
		step, ok := installStepTemplates[def.Lang]
		if !ok {
			return "", fmt.Errorf("runtimebuild: unknown language %q", def.Lang)
		}
		manifestDest = manifestDestPaths[def.Lang]
		installStep = fmt.Sprintf(step, manifestDest)
	}

	tmpl, err := template.New("containerfile").Parse(containerfileTemplate)
	if err != nil {
		return "", fmt.Errorf("runtimebuild: parsing template: %w", err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, struct {
		BaseImage         string
		SysPackages       string
		LangManifest      string
		InstallStep       string
		ManifestDest      string
		DisableDotnetIPv6 bool
	}{
		BaseImage:         def.BaseImage,
		SysPackages:       strings.Join(def.SysPackages, " "),
		LangManifest:      def.LangManifest,
		InstallStep:       installStep,
		ManifestDest:      manifestDest,
		DisableDotnetIPv6: def.Lang == store.LangPowerShell,
	})
	if err != nil {
		return "", fmt.Errorf("runtimebuild: rendering template: %w", err)
	}
	return buf.String(), nil
}

// BuildContext packs the rendered Containerfile and (if present) the
// language manifest into an in-memory tar, ready for podman.BuildImage.
// Never touches the host filesystem, for the same reason job script
// delivery doesn't (decision #24).
func BuildContext(def Definition) (*bytes.Buffer, error) {
	containerfile, err := Render(def)
	if err != nil {
		return nil, err
	}

	files := []podman.ArchiveFile{
		{Path: "Containerfile", Mode: 0o644, Content: []byte(containerfile)},
	}
	if def.LangManifest != "" {
		files = append(files, podman.ArchiveFile{
			Path: manifestPath, Mode: 0o644, Content: []byte(def.LangManifest),
		})
	}
	return podman.TarFiles(files)
}

// InputHash hashes exactly the fields that determine what gets built, so
// two runtimes with identical inputs dedupe onto the same image tag (task
// 4.4) even under different names or system-package orderings. Sorted
// packages make the hash independent of the order they were declared in.
func InputHash(def Definition) string {
	packages := append([]string(nil), def.SysPackages...)
	sort.Strings(packages)

	h := sha256.New()
	fmt.Fprintf(h, "base_image:%s\n", def.BaseImage)
	fmt.Fprintf(h, "sys_packages:%s\n", strings.Join(packages, ","))
	fmt.Fprintf(h, "lang:%s\n", def.Lang)
	fmt.Fprintf(h, "lang_manifest:%s\n", def.LangManifest)
	return hex.EncodeToString(h.Sum(nil))
}

// ImageTag is the tag a build is created and looked up under - local to
// this host, never pushed, so a short deterministic name plus the input
// hash is enough to dedupe without colliding.
func ImageTag(def Definition) string {
	return fmt.Sprintf("localhost/descendence-runtime:%s", InputHash(def))
}
