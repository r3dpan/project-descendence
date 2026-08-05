package manifest

import "testing"

func TestResolveParams(t *testing.T) {
	contract := []Param{
		{Name: "name", Type: ParamTypeString, Required: true},
		{Name: "shout", Type: ParamTypeBool, Required: false, Default: strPtr("false")},
		{Name: "count", Type: ParamTypeNumber, Required: false, Default: strPtr("1")},
	}

	t.Run("applies defaults and coerces types", func(t *testing.T) {
		got, err := ResolveParams(contract, map[string]string{"name": "World", "shout": "true"})
		if err != nil {
			t.Fatalf("ResolveParams: %v", err)
		}
		if got["name"] != "World" {
			t.Errorf("name = %v", got["name"])
		}
		if got["shout"] != true {
			t.Errorf("shout = %v", got["shout"])
		}
		if got["count"] != float64(1) {
			t.Errorf("count = %v", got["count"])
		}
	})

	t.Run("missing required param", func(t *testing.T) {
		_, err := ResolveParams(contract, map[string]string{})
		if err == nil {
			t.Fatal("ResolveParams accepted a submission missing a required param")
		}
	})

	t.Run("unknown param rejected", func(t *testing.T) {
		_, err := ResolveParams(contract, map[string]string{"name": "x", "bogus": "y"})
		if err == nil {
			t.Fatal("ResolveParams accepted an unknown param name")
		}
	})

	t.Run("bad type coercion rejected", func(t *testing.T) {
		_, err := ResolveParams(contract, map[string]string{"name": "x", "count": "not-a-number"})
		if err == nil {
			t.Fatal("ResolveParams accepted a non-numeric value for a number param")
		}
	})

	t.Run("no submission, no contract", func(t *testing.T) {
		got, err := ResolveParams(nil, nil)
		if err != nil {
			t.Fatalf("ResolveParams: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})
}
