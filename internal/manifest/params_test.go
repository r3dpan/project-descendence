package manifest

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestResolveParams(t *testing.T) {
	contract := []Param{
		{Name: "name", Type: ParamTypeString, Required: true},
		{Name: "shout", Type: ParamTypeBool, Required: false, Default: strPtr("false")},
		{Name: "count", Type: ParamTypeNumber, Required: false, Default: strPtr("1")},
	}

	t.Run("applies defaults, coerces types, preserves contract order", func(t *testing.T) {
		got, secrets, err := ResolveParams(contract, map[string]string{"name": "World", "shout": "true"})
		if err != nil {
			t.Fatalf("ResolveParams: %v", err)
		}
		if len(secrets) != 0 {
			t.Errorf("secrets = %+v, want none (no mount params in this contract)", secrets)
		}
		want := []ResolvedParam{
			{Name: "name", Value: "World"},
			{Name: "shout", Value: true},
			{Name: "count", Value: float64(1)},
		}
		if len(got) != len(want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("missing required param", func(t *testing.T) {
		_, _, err := ResolveParams(contract, map[string]string{})
		if err == nil {
			t.Fatal("ResolveParams accepted a submission missing a required param")
		}
	})

	t.Run("unknown param rejected", func(t *testing.T) {
		_, _, err := ResolveParams(contract, map[string]string{"name": "x", "bogus": "y"})
		if err == nil {
			t.Fatal("ResolveParams accepted an unknown param name")
		}
	})

	t.Run("bad type coercion rejected", func(t *testing.T) {
		_, _, err := ResolveParams(contract, map[string]string{"name": "x", "count": "not-a-number"})
		if err == nil {
			t.Fatal("ResolveParams accepted a non-numeric value for a number param")
		}
	})

	t.Run("no submission, no contract", func(t *testing.T) {
		got, secrets, err := ResolveParams(nil, nil)
		if err != nil {
			t.Fatalf("ResolveParams: %v", err)
		}
		if len(got) != 0 || len(secrets) != 0 {
			t.Errorf("got %v / %v, want both empty", got, secrets)
		}
	})

	t.Run("mount params are split into secrets, not params", func(t *testing.T) {
		withMount := append(contract, Param{Name: "token", Type: ParamTypeMount, Required: true})
		got, secrets, err := ResolveParams(withMount, map[string]string{"name": "x", "token": "sekrit"})
		if err != nil {
			t.Fatalf("ResolveParams: %v", err)
		}
		for _, p := range got {
			if p.Name == "token" {
				t.Errorf("mount param %q leaked into the non-secret result", p.Name)
			}
		}
		if len(secrets) != 1 || secrets[0].Name != "token" || secrets[0].Value != "sekrit" {
			t.Errorf("secrets = %+v, want [{token sekrit}]", secrets)
		}
	})
}

func TestMergeParamsForDelivery(t *testing.T) {
	contract := []Param{
		{Name: "name", Type: ParamTypeString, Required: true},
		{Name: "token", Type: ParamTypeMount, Required: true},
	}
	paramsJSON := []byte(`[{"name":"name","value":"World"}]`)

	got, err := MergeParamsForDelivery(contract, paramsJSON)
	if err != nil {
		t.Fatalf("MergeParamsForDelivery: %v", err)
	}

	var merged []ResolvedParam
	if err := json.Unmarshal(got, &merged); err != nil {
		t.Fatalf("decoding merged result: %v", err)
	}
	want := []ResolvedParam{
		{Name: "name", Value: "World"},
		{Name: "token", Value: "/run/job/secrets/token"},
	}
	if len(merged) != len(want) {
		t.Fatalf("got %+v, want %+v", merged, want)
	}
	for i := range want {
		if merged[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, merged[i], want[i])
		}
	}
}

// TestArgvShimRouting covers task 6.4's routing rule: a shim only enters
// argv when the job has params, names no explicit command, and its script
// extension is one a shim exists for.
func TestArgvShimRouting(t *testing.T) {
	withParams := []Param{{Name: "x", Type: ParamTypeString, Required: true}}

	for _, tc := range []struct {
		name string
		m    Manifest
		want []string
	}{
		{
			"params + recognised extension routes through the shim",
			Manifest{ScriptPath: "scripts/greet.sh", Params: withParams},
			[]string{"/run/job/shim.sh", "/run/job/greet.sh"},
		},
		{
			"no params leaves argv as the bare script",
			Manifest{ScriptPath: "scripts/greet.sh"},
			[]string{"/run/job/greet.sh"},
		},
		{
			"unrecognised extension opts out even with params",
			Manifest{ScriptPath: "scripts/greet.rb", Params: withParams},
			[]string{"/run/job/greet.rb"},
		},
		{
			"an explicit command wins over shimming",
			Manifest{ScriptPath: "scripts/greet.py", Params: withParams, Command: []string{"python3", "-u", "/run/job/greet.py"}},
			[]string{"python3", "-u", "/run/job/greet.py"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.m.Argv()
			if len(got) != len(tc.want) {
				t.Fatalf("Argv() = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("Argv()[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParamsArgv(t *testing.T) {
	// The exit check's own case: a value that looks like a shell injection
	// attempt must survive NUL-delimited round-tripping completely literal.
	src := `[{"name":"a","value":"World"},{"name":"b","value":"\"; rm -rf /; #"},{"name":"c","value":true},{"name":"d","value":1.5}]`

	got, err := ParamsArgv([]byte(src))
	if err != nil {
		t.Fatalf("ParamsArgv: %v", err)
	}

	want := []byte("World\x00\"; rm -rf /; #\x00true\x001.5\x00")
	if !bytes.Equal(got, want) {
		t.Errorf("ParamsArgv = %q, want %q", got, want)
	}
}
