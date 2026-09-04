package mcp

import (
	"errors"
	"strings"
	"testing"
)

// sampleSchema exercises every property shape the tools use.
func sampleSchema() Schema {
	return object([]string{"cluster"}, map[string]Property{
		"cluster":   text("The cluster."),
		"namespace": text("A namespace."),
		"scope":     choice("Role scope.", "namespace", "cluster"),
		"previous":  flag("Read the previous run."),
		"limit":     bounded("How many rows.", 1, 100, 20),
	})
}

func TestValidateAcceptsWhatTheSchemaDescribes(t *testing.T) {
	arguments := map[string]any{
		"cluster":   "staging",
		"namespace": "shop",
		"scope":     "cluster",
		"previous":  true,
		"limit":     float64(50),
	}

	if err := validate(sampleSchema(), arguments); err != nil {
		t.Fatalf("rejected a valid call: %v", err)
	}
}

func TestValidateRejectsWhatTheSchemaDoesNotDescribe(t *testing.T) {
	cases := map[string]struct {
		arguments map[string]any
		mentions  string
	}{
		"a missing required argument": {
			arguments: map[string]any{"namespace": "shop"},
			mentions:  "cluster",
		},
		"a required argument that is only whitespace": {
			arguments: map[string]any{"cluster": "  "},
			mentions:  "must not be empty",
		},
		// Ignoring it would answer a different question: list every namespace
		// when one was meant, and say nothing about having done so.
		"an unknown argument": {
			arguments: map[string]any{"cluster": "staging", "namepsace": "shop"},
			mentions:  "namepsace",
		},
		"a number where a string belongs": {
			arguments: map[string]any{"cluster": 1},
			mentions:  "must be a string",
		},
		"a string where a number belongs": {
			arguments: map[string]any{"cluster": "staging", "limit": "20"},
			mentions:  "must be a number",
		},
		// JSON has one number type, so "5.5 rows" arrives as a float and must
		// be refused rather than truncated into something nobody asked for.
		"a fractional integer": {
			arguments: map[string]any{"cluster": "staging", "limit": 5.5},
			mentions:  "whole number",
		},
		"an integer below the minimum": {
			arguments: map[string]any{"cluster": "staging", "limit": float64(0)},
			mentions:  "at least 1",
		},
		"an integer above the maximum": {
			arguments: map[string]any{"cluster": "staging", "limit": float64(1000)},
			mentions:  "at most 100",
		},
		"a value outside an enum": {
			arguments: map[string]any{"cluster": "staging", "scope": "global"},
			mentions:  "namespace, cluster",
		},
		"a string where a boolean belongs": {
			arguments: map[string]any{"cluster": "staging", "previous": "yes"},
			mentions:  "true or false",
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			err := validate(sampleSchema(), testCase.arguments)
			if err == nil {
				t.Fatalf("accepted %v", testCase.arguments)
			}

			var invalid *invalidArgumentError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T, want an invalidArgumentError so the server reports invalid params rather than a tool failure", err)
			}
			if !strings.Contains(err.Error(), testCase.mentions) {
				t.Errorf("message %q does not mention %q", err.Error(), testCase.mentions)
			}
		})
	}
}

// An explicit null is the same as an absent optional argument: some clients
// fill every declared property, and refusing a null would refuse a call that
// asked for nothing unusual.
func TestValidateTreatsAnExplicitNullOptionalAsAbsent(t *testing.T) {
	arguments := map[string]any{"cluster": "staging", "namespace": nil, "limit": nil}

	if err := validate(sampleSchema(), arguments); err != nil {
		t.Fatalf("rejected explicit nulls: %v", err)
	}
	if got := Arguments(arguments).String("namespace"); got != "" {
		t.Errorf("a null namespace read as %q", got)
	}
	if got := Arguments(arguments).Int("limit", 20); got != 20 {
		t.Errorf("a null limit read as %d, want the fallback 20", got)
	}
}

// A required argument that is present and null is still missing: it names no
// cluster, and reading it as the empty string would send the read at nothing.
func TestValidateRejectsARequiredArgumentThatIsNull(t *testing.T) {
	if err := validate(sampleSchema(), map[string]any{"cluster": nil}); err == nil {
		t.Fatal("a null required argument was accepted")
	}
}

func TestArgumentsTrimAndFallBack(t *testing.T) {
	arguments := Arguments{"cluster": "  staging  ", "limit": float64(7), "previous": true}

	if got := arguments.String("cluster"); got != "staging" {
		t.Errorf("String returned %q", got)
	}
	if got := arguments.String("absent"); got != "" {
		t.Errorf("an absent string returned %q", got)
	}
	if got := arguments.Int("limit", 20); got != 7 {
		t.Errorf("Int returned %d", got)
	}
	if got := arguments.Int("absent", 20); got != 20 {
		t.Errorf("an absent integer returned %d, want the fallback", got)
	}
	if !arguments.Bool("previous") || arguments.Bool("absent") {
		t.Error("Bool misread a flag")
	}
}
