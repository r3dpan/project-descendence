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
// Fields belonging to the format are **rejected with an error naming the
// phase** rather than accepted and ignored for as long as nothing honours
// them, so a manifest never describes behaviour the platform will not
// perform - "this system's failure mode is silence" (HISTORY, Phase 2).
// `runtime` (task 4.6), `params` (task 6.1) and `form` (task 7.8) have all
// gone through this the same way - a typed field replaces the raw yaml.Node
// placeholder and its entry is removed from the unimplemented-keys loop
// below. Nothing is left pending there today.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
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
// literal in each. params.json (task 6.3), its Bash convenience form
// params.argv, and the shim itself (task 6.4) all live in this same
// directory.
const ContainerScriptDir = "/run/job"

// ContainerParamsJSONPath is where a run's resolved params land (task 6.3),
// as JSON - the contract every language's shim (or a hand-rolled script
// that reads it directly) can rely on.
func ContainerParamsJSONPath() string { return path.Join(ContainerScriptDir, "params.json") }

// ContainerParamsArgvPath is params.json's Bash-only convenience form (task
// 6.4): the same values, NUL-delimited, in contract order, needing no JSON
// parser to consume.
func ContainerParamsArgvPath() string { return path.Join(ContainerScriptDir, "params.argv") }

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

// ShimLang identifies which shim (task 6.4) a script's own extension calls
// for - "sh", "py" or "ps1" - or false if the extension isn't one this
// platform ships a shim for, in which case the script runs directly with no
// param support (not an error: an unrecognised extension just opts out).
func ShimLang(scriptPath string) (string, bool) {
	switch path.Ext(scriptPath) {
	case ".sh":
		return "sh", true
	case ".py":
		return "py", true
	case ".ps1":
		return "ps1", true
	default:
		return "", false
	}
}

// ContainerShimPath is where the shim matching lang is delivered.
func ContainerShimPath(lang string) string {
	return path.Join(ContainerScriptDir, "shim."+lang)
}

// ContainerSecretPath is where a mount-type param's Podman secret (task
// 6.6) is mounted inside the container. Deterministic from the param's own
// name alone - the supervisor's secret creation and this package's
// params.json merge (MergeParamsForDelivery) never need to agree through
// anything but that name.
func ContainerSecretPath(paramName string) string {
	return path.Join(ContainerScriptDir, "secrets", paramName)
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

	// ImageRef is a plain OCI reference, used as-is. Exactly one of ImageRef
	// and RuntimeName is set (task 4.6) - the manifest names either an image
	// directly or a runtime, never both and never neither.
	ImageRef string

	// RuntimeName identifies a runtime by name (task 4.6). The caller
	// resolves it to a runtime row and its built image; this package knows
	// nothing about runtimes existing or being built, the same way it knows
	// nothing about which commit a script lives at.
	RuntimeName string

	// Command overrides the default invocation. Nil - the usual case -
	// means the script is delivered executable and argv is just its path,
	// so its shebang chooses the interpreter and the platform needs to know
	// nothing about languages.
	Command []string

	// TimeoutSeconds is nil when the manifest does not say, in which case
	// the platform default applies.
	TimeoutSeconds *int32

	// Params is the parameter contract (task 6.1), in the order the
	// manifest declared them - order matters, since a Bash shim (task 6.4)
	// forwards params as positional arguments in this same order.
	Params []Param

	// Form is layout metadata over Params (task 7.8): which params appear in
	// which section, in what order, with what label/help override. Nil means
	// the manifest declared no form: at all, in which case a renderer shows
	// every param in contract order. Purely presentational - it can group,
	// order and relabel, but never changes what a param is or how its value
	// is validated; ResolveParams (task 6.2) has no idea this type exists.
	Form []FormSection
}

// Param types. "mount" (task 6.6) delivers its value via a Podman secret
// file rather than in params.json - everything else is a JSON scalar.
const (
	ParamTypeString = "string"
	ParamTypeNumber = "number"
	ParamTypeBool   = "bool"
	ParamTypeMount  = "mount"
)

// paramTypes is the closed set validate() accepts - closed, per this
// package's own precedent (§4.5's third rule): an unknown key is an error,
// not something silently passed through.
var paramTypes = map[string]bool{
	ParamTypeString: true,
	ParamTypeNumber: true,
	ParamTypeBool:   true,
	ParamTypeMount:  true,
}

// paramNamePattern is deliberately stricter than namePattern: a param name
// is forwarded by a shim (task 6.4) as a Bash env var suffix, a Python
// argparse flag and a PowerShell parameter name, all at once, so it must be
// valid as all three - no dots or dashes, unlike a job name.
var paramNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Param is one entry in the parameter contract - what a job takes, not what
// a particular run supplied. Submitted values are resolved against this by
// ResolveParams (task 6.2).
type Param struct {
	Name string
	Type string // one of ParamTypeString/Number/Bool/Mount

	// Required is false whenever the manifest set `required: false`, or
	// whenever a Default is present - a param with a default is never
	// "missing", so requiring it in addition would be contradictory.
	Required bool

	// Default is the raw scalar text as written in the manifest, typed and
	// validated at submission time (task 6.2) rather than here - Param
	// itself carries no dependency on how a value gets parsed. Nil means
	// the manifest gave none.
	Default *string

	// Secret marks a param whose value is redacted from anything the API
	// returns about a run (task 6.5). Independent of Type == mount: a
	// plain string param can be marked secret too, and every mount-type
	// param is treated as secret regardless of this flag.
	Secret bool
}

// FormSection groups related params under a heading in the rendered form
// (task 7.8's `form:` key). Purely a layout hint - it never changes what a
// param is or how ResolveParams (task 6.2) validates a submitted value.
type FormSection struct {
	Title  string
	Help   string
	Fields []FormField
}

// FormField places one param in a section, optionally overriding its label
// or adding help text. ParamName always names an entry already declared in
// params: - validateForm checks that before this is ever built, so a
// renderer never needs to guard against a dangling reference.
type FormField struct {
	ParamName string
	Label     string // "" - a renderer falls back to ParamName
	Help      string
}

// Argv is what the container is told to execute.
//
// With no explicit command and no recognised shim extension, it is the
// script's own path and nothing else, so the script's shebang chooses its
// interpreter - that is what keeps the platform language-agnostic for a
// script with no params: adding a language means writing a script with the
// right shebang and naming an image that has it, and the orchestrator
// learns nothing new.
//
// When the manifest declares at least one param, names no explicit command,
// and the script's extension matches a shim (task 6.4: .sh/.py/.ps1), argv
// instead points at that shim with the script's own path as its argument -
// the shim (delivered alongside the script, ContainerShimPath) reads
// params.json/params.argv and re-execs the script with a native calling
// convention. A job with no params is left exactly as before: rewrapping
// invocation for nothing to deliver would be a pointless behaviour change
// for the common case. An explicit `command:` always wins over shimming
// too - it is the author taking control of invocation, which params support
// opts out of rather than fights.
//
// Always a []string, never joined into a shell string (task 1.11).
func (m *Manifest) Argv() []string {
	if len(m.Command) > 0 {
		return m.Command
	}
	if len(m.Params) > 0 {
		if lang, ok := ShimLang(m.ScriptPath); ok {
			return []string{ContainerShimPath(lang), ContainerScriptPath(m.ScriptPath)}
		}
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
	Runtime        string   `yaml:"runtime"`
	Command        []string `yaml:"command"`
	TimeoutSeconds *int32   `yaml:"timeoutSeconds"`

	// Params is the parameter contract (task 6.1).
	Params []rawParam `yaml:"params"`

	// Form is layout metadata over Params (task 7.8) - see rawForm. A
	// pointer, like TimeoutSeconds, so "absent" is distinguishable from an
	// explicitly empty block.
	Form *rawForm `yaml:"form"`
}

// rawParam mirrors one entry of the manifest's `params:` list. Required is a
// pointer so "absent" (default to true unless a default is given) is
// distinguishable from an explicit `required: false`.
type rawParam struct {
	Name     string  `yaml:"name"`
	Type     string  `yaml:"type"`
	Required *bool   `yaml:"required"`
	Default  *string `yaml:"default"`
	Secret   bool    `yaml:"secret"`
}

// rawForm mirrors the manifest's `form:` block (task 7.8): grouping and
// ordering over params:, never a second source of what a param is.
type rawForm struct {
	Sections []rawFormSection `yaml:"sections"`
}

type rawFormSection struct {
	Title  string         `yaml:"title"`
	Help   string         `yaml:"help"`
	Fields []rawFormField `yaml:"fields"`
}

// rawFormField accepts either a bare param name (the common case: just place
// it, keep its own label) or a mapping with label/help overrides - forcing
// every entry into the mapping shape would make the common case pure
// boilerplate.
type rawFormField struct {
	Name  string
	Label string
	Help  string
}

// UnmarshalYAML implements the scalar-or-mapping shape above by hand, since
// that is not something a plain struct tag can express. Strict about unknown
// keys in the mapping form to match this package's closed-set convention
// elsewhere (§4.5's third rule) - decoder.KnownFields(true) on the outer
// decoder does not reach into a node decoded this way, so it is re-enforced
// here rather than silently lost for this one shape.
func (f *rawFormField) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&f.Name)
	}

	var fields map[string]yaml.Node
	if err := node.Decode(&fields); err != nil {
		return fmt.Errorf("form field entry must be a param name or a mapping with name/label/help: %w", err)
	}
	for key, value := range fields {
		var dst *string
		switch key {
		case "name":
			dst = &f.Name
		case "label":
			dst = &f.Label
		case "help":
			dst = &f.Help
		default:
			return fmt.Errorf("form field entry has unknown key %q; expected name, label or help", key)
		}
		if err := value.Decode(dst); err != nil {
			return err
		}
	}
	return nil
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

	params, err := validateParams(manifestPath, raw.Params)
	if err != nil {
		return nil, err
	}

	form, err := validateForm(manifestPath, raw.Form, params)
	if err != nil {
		return nil, err
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

	// Exactly one of image/runtime, matching the database's
	// jobs_image_or_runtime_check (task 4.6) - "or" there, "exactly one"
	// here, because the manifest is the one place close enough to the author
	// to say which of the two they meant rather than silently preferring one.
	switch {
	case raw.Image != "" && raw.Runtime != "":
		return nil, newError(manifestPath, "image and runtime cannot both be set; a job runs in exactly one of a plain image or a built runtime")
	case raw.Image == "" && raw.Runtime == "":
		return nil, newError(manifestPath, "exactly one of image or runtime is required; image is a plain OCI reference such as docker.io/library/alpine:3.20, runtime names a runtime defined through the API")
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
		RuntimeName:    raw.Runtime,
		Command:        raw.Command,
		TimeoutSeconds: raw.TimeoutSeconds,
		Params:         params,
		Form:           form,
	}, nil
}

// validateParams turns the manifest's raw `params:` list into the contract
// (task 6.1). Order is preserved - it is what a Bash shim (task 6.4) uses to
// turn params.json into positional arguments, so silently reordering them
// here would be a correctness bug there, not just a cosmetic one.
func validateParams(manifestPath string, raw []rawParam) ([]Param, error) {
	if raw == nil {
		return nil, nil
	}

	seen := make(map[string]bool, len(raw))
	params := make([]Param, 0, len(raw))
	for i, p := range raw {
		if p.Name == "" {
			return nil, newError(manifestPath, "params[%d]: name is required", i)
		}
		if !paramNamePattern.MatchString(p.Name) {
			return nil, newError(manifestPath, "params[%d]: name %q must start with a letter or underscore and contain only letters, digits and underscores - it is forwarded as a Bash env var, a Python flag and a PowerShell parameter name, all at once", i, p.Name)
		}
		if seen[p.Name] {
			return nil, newError(manifestPath, "params[%d]: duplicate param name %q", i, p.Name)
		}
		seen[p.Name] = true

		if !paramTypes[p.Type] {
			return nil, newError(manifestPath, "params[%d] (%s): type %q is not one of string, number, bool, mount", i, p.Name, p.Type)
		}

		if p.Type == ParamTypeMount && p.Default != nil {
			return nil, newError(manifestPath, "params[%d] (%s): a mount param cannot have a default - its value always comes from a Podman secret supplied at run time", i, p.Name)
		}

		required := p.Required == nil || *p.Required
		if p.Default != nil {
			// A default makes "required" contradictory: the value is never
			// actually missing, it just falls back. Rather than silently
			// ignoring an explicit `required: true` alongside a default,
			// only the implicit case (required omitted) is relaxed here;
			// an explicit true stays an error below.
			if p.Required == nil {
				required = false
			} else if *p.Required {
				return nil, newError(manifestPath, "params[%d] (%s): required cannot be true when a default is set - a value with a default is never missing", i, p.Name)
			}
			if err := checkScalarType(p.Type, *p.Default); err != nil {
				return nil, newError(manifestPath, "params[%d] (%s): default %v", i, p.Name, err)
			}
		}

		params = append(params, Param{
			Name:     p.Name,
			Type:     p.Type,
			Required: required,
			Default:  p.Default,
			Secret:   p.Secret,
		})
	}
	return params, nil
}

// validateForm turns the manifest's raw `form:` block into layout metadata
// over the already-resolved params contract (task 7.8) - called after
// validateParams so every reference can be checked against real params
// rather than the raw, unvalidated list.
//
// form: is optional, and when present may be partial: a param it doesn't
// mention still exists, and a renderer is expected to show it too (appended
// after the declared sections, in contract order) rather than hide it. So
// what's checked here is only internal consistency, not coverage: every
// reference names a real param, no param is placed twice, and no section
// exists to say nothing.
func validateForm(manifestPath string, raw *rawForm, params []Param) ([]FormSection, error) {
	if raw == nil {
		return nil, nil
	}

	known := make(map[string]bool, len(params))
	for _, p := range params {
		known[p.Name] = true
	}

	if len(raw.Sections) == 0 {
		return nil, newError(manifestPath, "form: is present but declares no sections; omit form: entirely to fall back to declaration order")
	}

	seen := make(map[string]bool)
	sections := make([]FormSection, 0, len(raw.Sections))
	for i, rs := range raw.Sections {
		if len(rs.Fields) == 0 {
			return nil, newError(manifestPath, "form.sections[%d]: has no fields; a section with nothing to show should be removed", i)
		}

		fields := make([]FormField, 0, len(rs.Fields))
		for j, rf := range rs.Fields {
			if rf.Name == "" {
				return nil, newError(manifestPath, "form.sections[%d].fields[%d]: name is required", i, j)
			}
			if !known[rf.Name] {
				return nil, newError(manifestPath, "form.sections[%d].fields[%d]: %q is not declared in params:", i, j, rf.Name)
			}
			if seen[rf.Name] {
				return nil, newError(manifestPath, "form.sections[%d].fields[%d]: %q already appears earlier in form:", i, j, rf.Name)
			}
			seen[rf.Name] = true

			fields = append(fields, FormField{ParamName: rf.Name, Label: rf.Label, Help: rf.Help})
		}

		sections = append(sections, FormSection{Title: rs.Title, Help: rs.Help, Fields: fields})
	}

	return sections, nil
}

// checkScalarType validates a raw string against a param's declared type,
// shared between a manifest's `default:` (here) and a submitted value
// (ResolveParams, task 6.2), so the two can never disagree about what counts
// as a valid number or bool.
func checkScalarType(paramType, value string) error {
	switch paramType {
	case ParamTypeString:
		return nil
	case ParamTypeNumber:
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Errorf("%q is not a valid number", value)
		}
		return nil
	case ParamTypeBool:
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("%q is not a valid bool (use true/false)", value)
		}
		return nil
	case ParamTypeMount:
		// A mount value is an opaque string - the secret's literal content -
		// with no shape of its own to validate, the same as ParamTypeString.
		// A mount param having a `default:` in the manifest is rejected
		// separately and earlier, in validateParams, before this is ever
		// reached for one.
		return nil
	default:
		return fmt.Errorf("unknown param type %q", paramType)
	}
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
