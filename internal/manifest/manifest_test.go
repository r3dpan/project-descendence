package manifest

import (
	"strings"
	"testing"
)

const validManifest = `
apiVersion: descendence/v1
name: backup-db
description: Nightly database dump
script: backup-db.sh
image: docker.io/library/alpine:3.20
`

func TestParseValid(t *testing.T) {
	got, err := Parse("scripts/backup-db.job.yaml", []byte(validManifest))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got.Name != "backup-db" {
		t.Errorf("Name = %q, want backup-db", got.Name)
	}
	if got.Description != "Nightly database dump" {
		t.Errorf("Description = %q", got.Description)
	}
	// Resolved relative to the manifest's directory, not the repo root.
	if got.ScriptPath != "scripts/backup-db.sh" {
		t.Errorf("ScriptPath = %q, want scripts/backup-db.sh", got.ScriptPath)
	}
	if got.ImageRef != "docker.io/library/alpine:3.20" {
		t.Errorf("ImageRef = %q", got.ImageRef)
	}
	if got.Command != nil {
		t.Errorf("Command = %v, want nil so the shebang decides", got.Command)
	}
	if got.TimeoutSeconds != nil {
		t.Errorf("TimeoutSeconds = %v, want nil so the platform default applies", *got.TimeoutSeconds)
	}
}

func TestParseOptionalFields(t *testing.T) {
	src := `
apiVersion: descendence/v1
name: backup-db
script: backup-db.sh
image: alpine:3.20
command: ["sh", "-eu", "/run/job/backup-db.sh"]
timeoutSeconds: 1800
`
	got, err := Parse("backup-db.job.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(got.Command) != 3 || got.Command[0] != "sh" {
		t.Errorf("Command = %v", got.Command)
	}
	if got.TimeoutSeconds == nil || *got.TimeoutSeconds != 1800 {
		t.Errorf("TimeoutSeconds = %v, want 1800", got.TimeoutSeconds)
	}
	// A manifest at the repository root resolves its script at the root too.
	if got.ScriptPath != "backup-db.sh" {
		t.Errorf("ScriptPath = %q, want backup-db.sh", got.ScriptPath)
	}
}

// TestParseScriptPathResolution pins the sidecar rule down, because it is the
// one thing about this format that is genuinely surprising if you expect
// repository-root-relative paths.
func TestParseScriptPathResolution(t *testing.T) {
	for _, tc := range []struct {
		manifestPath string
		script       string
		want         string
	}{
		{"backup.job.yaml", "backup.sh", "backup.sh"},
		{"scripts/backup.job.yaml", "backup.sh", "scripts/backup.sh"},
		{"a/b/c/backup.job.yaml", "backup.sh", "a/b/c/backup.sh"},
		{"scripts/backup.job.yaml", "bin/backup.sh", "scripts/bin/backup.sh"},
		{"a/b/backup.job.yaml", "../shared.sh", "a/shared.sh"},
	} {
		src := "apiVersion: descendence/v1\nname: j\nimage: alpine\nscript: " + tc.script + "\n"
		got, err := Parse(tc.manifestPath, []byte(src))
		if err != nil {
			t.Errorf("Parse(%s, script=%s): %v", tc.manifestPath, tc.script, err)
			continue
		}
		if got.ScriptPath != tc.want {
			t.Errorf("Parse(%s, script=%s).ScriptPath = %q, want %q", tc.manifestPath, tc.script, got.ScriptPath, tc.want)
		}
	}
}

// TestParseRejects is the bulk of this package's value: every way a manifest
// can be wrong should be a legible error rather than a job that runs something
// other than what the file says.
func TestParseRejects(t *testing.T) {
	for _, tc := range []struct {
		name     string
		src      string
		wantText string
	}{
		{
			name:     "empty file",
			src:      "",
			wantText: "empty",
		},
		{
			name:     "missing apiVersion",
			src:      "name: j\nscript: j.sh\nimage: alpine\n",
			wantText: "apiVersion is required",
		},
		{
			name:     "unsupported apiVersion",
			src:      "apiVersion: descendence/v2\nname: j\nscript: j.sh\nimage: alpine\n",
			wantText: "is not supported",
		},
		{
			name:     "unknown key",
			src:      "apiVersion: descendence/v1\nname: j\nscript: j.sh\nimage: alpine\niamge: typo\n",
			wantText: "iamge",
		},
		{
			name:     "missing name",
			src:      "apiVersion: descendence/v1\nscript: j.sh\nimage: alpine\n",
			wantText: "name is required",
		},
		{
			name:     "name with a slash",
			src:      "apiVersion: descendence/v1\nname: a/b\nscript: j.sh\nimage: alpine\n",
			wantText: "must start with a letter or digit",
		},
		{
			name:     "missing script",
			src:      "apiVersion: descendence/v1\nname: j\nimage: alpine\n",
			wantText: "script is required",
		},
		{
			name:     "missing image",
			src:      "apiVersion: descendence/v1\nname: j\nscript: j.sh\n",
			wantText: "image is required",
		},
		{
			name:     "absolute script path",
			src:      "apiVersion: descendence/v1\nname: j\nscript: /etc/passwd\nimage: alpine\n",
			wantText: "must be relative",
		},
		{
			name:     "script escaping the repository",
			src:      "apiVersion: descendence/v1\nname: j\nscript: ../../outside.sh\nimage: alpine\n",
			wantText: "outside the repository",
		},
		{
			name:     "empty command",
			src:      "apiVersion: descendence/v1\nname: j\nscript: j.sh\nimage: alpine\ncommand: []\n",
			wantText: "omit it entirely",
		},
		{
			name:     "non-positive timeout",
			src:      "apiVersion: descendence/v1\nname: j\nscript: j.sh\nimage: alpine\ntimeoutSeconds: 0\n",
			wantText: "must be positive",
		},
		{
			name:     "two documents",
			src:      "apiVersion: descendence/v1\nname: j\nscript: j.sh\nimage: alpine\n---\napiVersion: descendence/v1\nname: k\nscript: k.sh\nimage: alpine\n",
			wantText: "more than one YAML document",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse("scripts/x.job.yaml", []byte(tc.src))
			if err == nil {
				t.Fatalf("Parse accepted a manifest it must reject")
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantText)
			}
			// Every error must name the file, since a scan reports many.
			if !strings.Contains(err.Error(), "scripts/x.job.yaml") {
				t.Errorf("error = %q, want it to name the manifest path", err)
			}
		})
	}
}

// TestParseRejectsUnimplementedSections is the point of "specify whole,
// implement subset". These keys are part of the format and will work later;
// accepting them now would mean a manifest that describes behaviour the
// platform silently does not perform.
func TestParseRejectsUnimplementedSections(t *testing.T) {
	for _, tc := range []struct{ key, src, wantPhase string }{
		{
			key:       "runtime",
			src:       "apiVersion: descendence/v1\nname: j\nscript: j.sh\nimage: alpine\nruntime: python-3.12\n",
			wantPhase: "Phase 4",
		},
		{
			key:       "params",
			src:       "apiVersion: descendence/v1\nname: j\nscript: j.sh\nimage: alpine\nparams:\n  - name: db\n    type: string\n",
			wantPhase: "Phase 6",
		},
		{
			key:       "form",
			src:       "apiVersion: descendence/v1\nname: j\nscript: j.sh\nimage: alpine\nform:\n  - field: db\n    widget: text\n",
			wantPhase: "Phase 7",
		},
	} {
		t.Run(tc.key, func(t *testing.T) {
			_, err := Parse("x.job.yaml", []byte(tc.src))
			if err == nil {
				t.Fatalf("Parse accepted `%s:`, which nothing honours yet", tc.key)
			}
			// The distinction that matters: this is not "unknown key", it is
			// "known key, not yet honoured", and the message says when.
			if !strings.Contains(err.Error(), tc.wantPhase) {
				t.Errorf("error = %q, want it to name %s", err, tc.wantPhase)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error = %q, want it to name the key", err)
			}
		})
	}
}

// TestErrorCarriesPath is what lets a scan over many manifests report which
// one failed without threading the path through every call site (task 3.4).
func TestErrorCarriesPath(t *testing.T) {
	_, err := Parse("deeply/nested/broken.job.yaml", []byte("apiVersion: descendence/v1\n"))
	if err == nil {
		t.Fatal("Parse accepted a manifest with no name")
	}
	var manifestErr *Error
	if !asManifestError(err, &manifestErr) {
		t.Fatalf("error is %T, want *manifest.Error", err)
	}
	if manifestErr.Path != "deeply/nested/broken.job.yaml" {
		t.Errorf("Error.Path = %q", manifestErr.Path)
	}
}

func asManifestError(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}
