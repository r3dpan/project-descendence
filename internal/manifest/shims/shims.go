// Package shims holds the small per-language wrapper a job's argv points at
// instead of its script directly, once the job declares at least one param
// (task 6.4, ARCHITECTURE.md §4.7). Each shim reads what the supervisor
// delivered alongside the script - params.json (every language) or
// params.argv (Bash only, manifest.ParamsArgv's NUL-delimited convenience) -
// and re-execs the real script with a native calling convention, so the
// core orchestrator never needs to know a parameter exists, only that a
// contract does.
package shims

import _ "embed"

// Content, keyed by manifest.ShimLang's identifier ("sh", "py", "ps1").
var (
	//go:embed shim.sh
	Bash []byte

	//go:embed shim.py
	Python []byte

	//go:embed shim.ps1
	PowerShell []byte
)

// ByLang maps a manifest.ShimLang identifier to its embedded content, for
// the supervisor to deliver the right one without duplicating the mapping.
var ByLang = map[string][]byte{
	"sh":  Bash,
	"py":  Python,
	"ps1": PowerShell,
}
