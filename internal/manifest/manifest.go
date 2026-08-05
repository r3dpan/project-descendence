// Package manifest parses and validates the sidecar job manifest -
// `<name>.job.yaml`, living beside the script it describes (ARCHITECTURE.md
// §4.5, decision #7).
//
// The manifest *is* the job. A job is a script's interface - what it takes,
// how it is presented, how it is invoked - and all of that is only correct
// relative to a particular version of the script, which is why it is authored
// in git and versioned alongside it rather than stored in Postgres. The `jobs`
// table is a projection of what a scan finds here.
//
// # The format is specified whole and implemented in parts
//
// `params`, `form` and `runtime` belong to the format now and are the reason
// the format exists at all, but nothing honours them until Phases 6, 7 and 4
// respectively. They are therefore **rejected with an error naming the phase**
// rather than accepted and ignored. A manifest that says `runtime: python-3.12`
// and quietly runs Alpine would be this project's documented failure mode -
// "this system's failure mode is silence" (HISTORY, Phase 2) - and the whole
// point of an interface is that it does not lie about the script.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	// Suffix is what a scan looks for. Manifests sit beside their scripts,
	// so there is no fixed directory to search (task 3.4).
	Suffix = ".job.yaml"

	// APIVersion is the only format version that exists. It is required
	// rather than defaulted: a manifest committed today will still be read
	// years from now, and a file that does not say what it is cannot be
	// migrated safely later.
	APIVersion = "descendence/v1"
)

// ContainerScriptDir is where a job's script is placed inside its container.
//
// The API turns this into the run's argv when the run is created, and the
// supervisor unpacks the script here when the container is created. Those are
// two processes that never talk to each other (§3), so the agreement between
// them has to live in one place - this constant - rather than as a string
// literal in each. Phase 6's params.json is destined for the same directory.
const ContainerScriptDir = "/run/job"

// namePattern constrains a job name because the name is how a job is
// addressed - `descendence jobs run <name>` - and is unique among live jobs.
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ContainerScriptPath is where scriptPath will be found inside the container.
// Only the base name survives: where a script sits in the repository is the
// repository's business, and flattening it means a manifest cannot smuggle a
// directory layout into the container.
func ContainerScriptPath(scriptPath string) string {
	return path.Join(ContainerScriptDir, path.Base(scriptPath))
}

// Manifest is a validated job definition, with paths already resolved
// relative to the repository root.
type Manifest struct {
	// Name identifies the job and must be unique among live jobs. The
	// filename is convention, not authority - this field decides.
	Name string

	// Description is free text shown in listings.
	Description string

	// ScriptPath is repository-root-relative, resolved from the manifest's
	// own directory. See resolveScriptPath.
	ScriptPath string

	// ImageRef is a plain OCI reference, used as-is. Phase 4 introduces
	// runtimes as the alternative to naming an image directly.
	ImageRef string

	// Command overrides the default invocation. Nil - the usual case -
	// means the script is delivered executable and argv is just its path,
	// so its shebang chooses the interpreter and the platform needs to know
	// nothing about languages.
	Command []string

	// TimeoutSeconds is nil when the manifest does not say, in which case
	// the platform default applies.
	TimeoutSeconds *int32
}

// Argv is what the container is told to execute.
//
// With no explicit command - the usual case - it is the script's own path and
// nothing else, so the script's shebang chooses its interpreter. That is what
// keeps the platform language-agnostic: adding a language means writing a
// script with the right shebang and naming an image that has it, and the
// orchestrator learns nothing new.
//
// Always a []string, never joined into a shell string (task 1.11).
func (m *Manifest) Argv() []string {
	if len(m.Command) > 0 {
		return m.Command
	}
	return []string{ContainerScriptPath(m.ScriptPath)}
}

// file mirrors the YAML document. The unimplemented sections are declared here
// as raw nodes purely so that strict decoding accepts them and the validator
// can then reject them with a better message than "field not found".
type file struct {
	APIVersion     string   `yaml:"apiVersion"`
	Name           string   `yaml:"name"`
	Description    string   `yaml:"description"`
	Script         string   `yaml:"script"`
	Image          string   `yaml:"image"`
	Command        []string `yaml:"command"`
	TimeoutSeconds *int32   `yaml:"timeoutSeconds"`

	// Specified by the format, honoured later. See the package comment.
	// Raw nodes rather than typed fields: their shape is not settled yet, so
	// decoding them into anything specific would be inventing a schema that
	// the phase which implements them has to live with. A zero Kind means
	// the key was absent.
	Runtime yaml.Node `yaml:"runtime"`
	Params  yaml.Node `yaml:"params"`
	Form    yaml.Node `yaml:"form"`
}

// Error is a manifest that could not be used, carrying the path so that a
// scan over many files can report which one was at fault without the caller
// having to thread it through (task 3.4).
type Error struct {
	Path   string
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Detail)
}

func newError(manifestPath, format string, args ...any) *Error {
	return &Error{Path: manifestPath, Detail: fmt.Sprintf(format, args...)}
}

// Parse reads one manifest. manifestPath is the file's repository-root-relative
// location, used both for error messages and to resolve the script path.
func Parse(manifestPath string, data []byte) (*Manifest, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))

	// Strict: a key the format does not define is an error, not something to
	// skip past. A typo like `iamge:` would otherwise leave the job running
	// on whatever image a previous sync recorded, which is the silent-wrongness
	// this format is trying to avoid.
	decoder.KnownFields(true)

	var raw file
	if err := decoder.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, newError(manifestPath, "file is empty")
		}
		// yaml's own errors carry line numbers; keep them verbatim.
		return nil, newError(manifestPath, "%v", err)
	}

	// A second document in the same file would be silently ignored otherwise.
	var extra yaml.Node
	if err := decoder.Decode(&extra); err == nil {
		return nil, newError(manifestPath, "file contains more than one YAML document; a manifest defines exactly one job")
	}

	return validate(manifestPath, &raw)
}

func validate(manifestPath string, raw *file) (*Manifest, error) {
	// Version first: every message below assumes v1 semantics, so saying
	// "name is required" about a v2 file would be misleading.
	switch raw.APIVersion {
	case "":
		return nil, newError(manifestPath, "apiVersion is required and must be %q", APIVersion)
	case APIVersion:
	default:
		return nil, newError(manifestPath, "apiVersion %q is not supported; this platform reads %q", raw.APIVersion, APIVersion)
	}

	// Specified-but-unimplemented, before anything else that might mask it.
	// The error names the phase so the answer to "when?" is in the message
	// rather than in a document the reader has to go and find.
	for _, unimplemented := range []struct {
		key     string
		node    yaml.Node
		phase   string
		purpose string
	}{
		{"runtime", raw.Runtime, "Phase 4", "runtimes are built images; until then a manifest names an image directly with `image:`"},
		{"params", raw.Params, "Phase 6", "the parameter contract is not yet enforced or passed to scripts"},
		{"form", raw.Form, "Phase 7", "form layout is not yet rendered by anything"},
	} {
		if unimplemented.node.Kind != 0 {
			return nil, newError(manifestPath,
				"`%s:` belongs to the manifest format but is not honoured until %s - %s. Remove it rather than leaving it inert, so this manifest does not describe behaviour the platform will not perform",
				unimplemented.key, unimplemented.phase, unimplemented.purpose)
		}
	}

	if raw.Name == "" {
		return nil, newError(manifestPath, "name is required")
	}
	if len(raw.Name) > 64 {
		return nil, newError(manifestPath, "name %q is longer than 64 characters", raw.Name)
	}
	if !namePattern.MatchString(raw.Name) {
		return nil, newError(manifestPath, "name %q must start with a letter or digit and contain only letters, digits, dots, dashes and underscores - it is how the job is addressed on the command line", raw.Name)
	}

	if raw.Script == "" {
		return nil, newError(manifestPath, "script is required")
	}
	scriptPath, err := resolveScriptPath(manifestPath, raw.Script)
	if err != nil {
		return nil, newError(manifestPath, "%v", err)
	}

	// Required in v1 because `runtime:` - the alternative - is not honoured
	// yet. The database constraint is the weaker "image or runtime", which
	// is what Phase 4 will relax this to.
	if raw.Image == "" {
		return nil, newError(manifestPath, "image is required; it is a plain OCI reference such as docker.io/library/alpine:3.20")
	}

	if raw.Command != nil && len(raw.Command) == 0 {
		return nil, newError(manifestPath, "command is present but empty; omit it entirely to let the script's shebang choose the interpreter")
	}
	for i, arg := range raw.Command {
		if arg == "" {
			return nil, newError(manifestPath, "command[%d] is an empty string", i)
		}
	}

	if raw.TimeoutSeconds != nil && *raw.TimeoutSeconds <= 0 {
		return nil, newError(manifestPath, "timeoutSeconds must be positive, got %d", *raw.TimeoutSeconds)
	}

	return &Manifest{
		Name:           raw.Name,
		Description:    raw.Description,
		ScriptPath:     scriptPath,
		ImageRef:       raw.Image,
		Command:        raw.Command,
		TimeoutSeconds: raw.TimeoutSeconds,
	}, nil
}

// resolveScriptPath interprets `script:` **relative to the manifest's own
// directory**, not to the repository root.
//
// That is what "sidecar" means (§4.5): a manifest and its script are one unit,
// and a directory holding both can be moved, copied as a starting point for
// the next job, or vendored from another repository without every path inside
// it having to be rewritten. The cost is that a manifest at the repository
// root and one nested three deep read the same, which is the intended
// property rather than an accident.
func resolveScriptPath(manifestPath, script string) (string, error) {
	if path.IsAbs(script) {
		return "", fmt.Errorf("script %q must be relative to the manifest, not absolute", script)
	}

	resolved := path.Join(path.Dir(manifestPath), script)

	// path.Join has already cleaned the result, so a "../" that escapes the
	// repository shows up here as a leading "..".
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", fmt.Errorf("script %q resolves to %q, which is outside the repository", script, resolved)
	}
	if resolved == "." || resolved == "" {
		return "", fmt.Errorf("script %q does not name a file", script)
	}

	return resolved, nil
}
