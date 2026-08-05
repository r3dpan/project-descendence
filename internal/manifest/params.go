package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// ResolvedParam is one param after ResolveParams: its name and its typed
// value (string/float64/bool). A slice, not a map - order is preserved in
// contract order, because task 6.4's Bash shim turns this same order into
// positional arguments, and a JSON *object*'s key order is not something Go
// (or the JSON spec) makes any promise about preserving.
type ResolvedParam struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// ResolveParams validates a run request's submitted values against a job's
// parameter contract (task 6.2) and returns the values to store on the run
// and deliver to the container - typed (string/float64/bool), coerced from
// the raw strings a submission arrives as (CLI flags and JSON request
// bodies both carry strings; the contract's declared type is what decides
// what a value *means*).
//
// Every error here is meant to become an HTTP 400: nothing is queued until
// submitted values are known to satisfy the contract, so a malformed
// request never reaches the supervisor.
//
// mount-type params (task 6.6's mechanism) are resolved here like any other
// param - the raw string stored in the result is turned into an actual
// Podman secret only by the supervisor. Until task 6.6, that means a
// mount-type value's plaintext sits in the same result this returns for
// every other param; task 6.5 redacts it from anything the API returns
// about the run, but storage doesn't split it out until 6.6 gives it a
// dedicated column.
func ResolveParams(contract []Param, submitted map[string]string) ([]ResolvedParam, error) {
	byName := make(map[string]Param, len(contract))
	for _, p := range contract {
		byName[p.Name] = p
	}

	for name := range submitted {
		if _, ok := byName[name]; !ok {
			return nil, fmt.Errorf("unknown param %q; this job accepts %s", name, contractNames(contract))
		}
	}

	resolved := make([]ResolvedParam, 0, len(contract))
	for _, p := range contract {
		raw, submittedOK := submitted[p.Name]
		switch {
		case submittedOK:
			value, err := coerceParam(p.Type, raw)
			if err != nil {
				return nil, fmt.Errorf("param %q: %w", p.Name, err)
			}
			resolved = append(resolved, ResolvedParam{Name: p.Name, Value: value})
		case p.Default != nil:
			value, err := coerceParam(p.Type, *p.Default)
			if err != nil {
				// The manifest's own default failing its own type is a
				// validateParams bug, not a caller error - but surfacing it
				// beats silently dropping the param.
				return nil, fmt.Errorf("param %q: manifest default is invalid: %w", p.Name, err)
			}
			resolved = append(resolved, ResolvedParam{Name: p.Name, Value: value})
		case p.Required:
			return nil, fmt.Errorf("missing required param %q", p.Name)
		}
	}
	return resolved, nil
}

// coerceParam turns a raw submitted or default string into the typed value
// stored in params.json and returned to the API - shares checkScalarType's
// notion of "valid" so a value accepted here is exactly one Parse's own
// default-validation would also accept.
func coerceParam(paramType, raw string) (any, error) {
	if err := checkScalarType(paramType, raw); err != nil {
		return nil, err
	}
	switch paramType {
	case ParamTypeNumber:
		f, _ := strconv.ParseFloat(raw, 64)
		return f, nil
	case ParamTypeBool:
		b, _ := strconv.ParseBool(raw)
		return b, nil
	default: // string, mount
		return raw, nil
	}
}

// ParamsArgv turns a run's stored params.json back into the Bash shim's
// NUL-delimited convenience form (task 6.4): one stringified value per
// param, in contract order, each terminated with a NUL byte. This is what
// lets the Bash shim skip writing a JSON parser entirely - `mapfile -d ''`
// reads it straight into an array - and it's exact for any byte a param
// value can hold (quotes, newlines, anything but a literal NUL), unlike any
// escaping scheme.
func ParamsArgv(paramsJSON []byte) ([]byte, error) {
	var params []ResolvedParam
	if len(paramsJSON) > 0 {
		if err := json.Unmarshal(paramsJSON, &params); err != nil {
			return nil, fmt.Errorf("decoding params.json: %w", err)
		}
	}
	var buf bytes.Buffer
	for _, p := range params {
		fmt.Fprintf(&buf, "%v", p.Value)
		buf.WriteByte(0)
	}
	return buf.Bytes(), nil
}

func contractNames(contract []Param) string {
	names := make([]string, len(contract))
	for i, p := range contract {
		names[i] = p.Name
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "no params"
	}
	return fmt.Sprintf("%v", names)
}
