package runtimebuild

import (
	"strings"
	"testing"

	"github.com/r3dpan/project-descendence/internal/store"
)

func TestRenderPython(t *testing.T) {
	def := Definition{
		BaseImage:    CuratedBaseImages[store.LangPython],
		SysPackages:  []string{"curl"},
		Lang:         store.LangPython,
		LangManifest: "requests==2.32.3\n",
	}
	out, err := Render(def)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"FROM " + def.BaseImage,
		"apt-get install -y --no-install-recommends curl",
		"COPY manifest /tmp/manifest",
		"pip install --no-cache-dir -r /tmp/manifest",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered Containerfile missing %q, got:\n%s", want, out)
		}
	}
}

func TestRenderNoManifestSkipsInstallStep(t *testing.T) {
	def := Definition{BaseImage: "docker.io/library/debian:12", Lang: store.LangPython}
	out, err := Render(def)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "COPY manifest") || strings.Contains(out, "pip install") {
		t.Errorf("expected no install step for an empty manifest, got:\n%s", out)
	}
}

func TestRenderUnknownLangWithManifestErrors(t *testing.T) {
	def := Definition{BaseImage: "docker.io/library/debian:12", Lang: "ruby", LangManifest: "x"}
	if _, err := Render(def); err == nil {
		t.Fatal("expected an error for an unknown language, got nil")
	}
}

func TestInputHashStableUnderPackageOrder(t *testing.T) {
	a := Definition{BaseImage: "b", SysPackages: []string{"x", "y"}, Lang: store.LangPython, LangManifest: "m"}
	b := Definition{BaseImage: "b", SysPackages: []string{"y", "x"}, Lang: store.LangPython, LangManifest: "m"}
	if InputHash(a) != InputHash(b) {
		t.Error("expected InputHash to be independent of sys_packages order")
	}
}

func TestInputHashChangesWithManifest(t *testing.T) {
	a := Definition{BaseImage: "b", Lang: store.LangPython, LangManifest: "requests==2.32.3"}
	b := Definition{BaseImage: "b", Lang: store.LangPython, LangManifest: "requests==2.32.4"}
	if InputHash(a) == InputHash(b) {
		t.Error("expected InputHash to change when the manifest content changes")
	}
}

func TestBuildContextIncludesRenderedFiles(t *testing.T) {
	def := Definition{
		BaseImage:    "docker.io/library/python:3.12-slim-bookworm",
		Lang:         store.LangPython,
		LangManifest: "requests==2.32.3\n",
	}
	buf, err := BuildContext(def)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected a non-empty tar archive")
	}
}
