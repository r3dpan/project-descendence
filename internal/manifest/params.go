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
// parameter contract (task 6.2) and returns two ordered, contract-order
// slices: params for every non-mount param (what's stored in
// runs.params_json and, redaction aside, what the API returns) and secrets
// for every mount-type one (runs.secret_params_json, task 6.6 - never
// returned by an API response, only read by the supervisor to create
// Podman secrets). Splitting here, at the one place a submission becomes
// stored values, is what makes "a mount value never reaches params_json"
// true from the moment a run exists rather than something a later step has
// to remember to enforce.
//
// Values are typed (string/float64/bool), coerced from the raw strings a
// submission arrives as (CLI flags and JSON request bodies both carry
// strings; the contract's declared type is what decides what a value
// *means*) - a mount value is always a string, the secret's literal
// content.
//
// Every error here is meant to become an HTTP 400: nothing is queued until
// submitted values are known to satisfy the contract, so a malformed
// request never reaches the supervisor.
func ResolveParams(contract []Param, submitted map[string]string) (params []ResolvedParam, secrets []ResolvedParam, err error) {
	byName := make(map[string]Param, len(contract))
	for _, p := range contract {
		byName[p.Name] = p
	}

	for name := range submitted {
		if _, ok := byName[name]; !ok {
			return nil, nil, fmt.Errorf("unknown param %q; this job accepts %s", name, contractNames(contract))
		}
	}

	for _, p := range contract {
		raw, submittedOK := submitted[p.Name]
		var value any
		switch {
		case submittedOK:
			v, err := coerceParam(p.Type, raw)
			if err != nil {
				return nil, nil, fmt.Errorf("param %q: %w", p.Name, err)
			}
			value = v
		case p.Default != nil:
			v, err := coerceParam(p.Type, *p.Default)
			if err != nil {
				// The manifest's own default failing its own type is a
				// validateParams bug, not a caller error - but surfacing it
				// beats silently dropping the param.
				return nil, nil, fmt.Errorf("param %q: manifest default is invalid: %w", p.Name, err)
			}
			value = v
		case p.Required:
			return nil, nil, fmt.Errorf("missing required param %q", p.Name)
		default:
			continue // optional, no default, not submitted: simply absent
		}

		resolved := ResolvedParam{Name: p.Name, Value: value}
		if p.Type == ParamTypeMount {
			secrets = append(secrets, resolved)
		} else {
			params = append(params, resolved)
		}
	}
	return params, secrets, nil
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

// MergeParamsForDelivery rebuilds the full, contract-ordered params.json
// that goes into a container (task 6.3/6.6): non-mount values come from
// paramsJSON (runs.params_json, task 6.2's split), and every mount-type
// entry's value is the path its Podman secret is mounted at
// (ContainerSecretPath) - never the secret's plaintext, which this
// function never even sees, since runs.params_json never held it in the
// first place (ResolveParams already split it into secret_params_json).
// Contract order, not either input's order, decides the result's order -
// what the Bash shim (task 6.4) turns into positional arguments.
func MergeParamsForDelivery(contract []Param, paramsJSON []byte) ([]byte, error) {
	var params []ResolvedParam
	if len(paramsJSON) > 0 {
		if err := json.Unmarshal(paramsJSON, &params); err != nil {
			return nil, fmt.Errorf("decoding params.json: %w", err)
		}
	}
	byName := make(map[string]any, len(params))
	for _, p := range params {
		byName[p.Name] = p.Value
	}

	merged := make([]ResolvedParam, 0, len(contract))
	for _, p := range contract {
		if p.Type == ParamTypeMount {
			merged = append(merged, ResolvedParam{Name: p.Name, Value: ContainerSecretPath(p.Name)})
			continue
		}
		if v, ok := byName[p.Name]; ok {
			merged = append(merged, ResolvedParam{Name: p.Name, Value: v})
		}
		// Absent: an optional param with neither a submission nor a
		// default, which ResolveParams already leaves out of paramsJSON.
	}
	return json.Marshal(merged)
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
