package mcp

import (
	"fmt"
	"slices"
	"strings"
)

// Schema is the JSON Schema a tool's arguments are validated against.
//
// Hand-written rather than reflected off a Go struct because the DESCRIPTIONS
// are the interface: a model chooses a tool and fills its arguments from
// these sentences and nothing else, so they carry the things a struct tag
// cannot — that a namespace left out means every namespace, that a kind is
// named the way the catalogue names it, that a log read is capped.
type Schema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
	// AdditionalProperties is always false. A misspelt argument that is
	// silently ignored produces a plausible answer to a question nobody
	// asked — the pod list for every namespace when one was meant — which is
	// far worse than being told the name was wrong.
	AdditionalProperties bool `json:"additionalProperties"`
}

// Property describes one argument.
type Property struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
	Minimum     *int64   `json:"minimum,omitempty"`
	Maximum     *int64   `json:"maximum,omitempty"`
	Default     any      `json:"default,omitempty"`
}

// object builds a schema from its properties and required keys.
func object(required []string, properties map[string]Property) Schema {
	return Schema{
		Type:                 "object",
		Properties:           properties,
		Required:             required,
		AdditionalProperties: false,
	}
}

// text describes a required or optional string argument.
func text(description string) Property {
	return Property{Type: "string", Description: description}
}

// choice describes a string argument limited to a fixed set.
func choice(description string, values ...string) Property {
	return Property{Type: "string", Description: description, Enum: values}
}

// flag describes a boolean argument.
func flag(description string) Property {
	return Property{Type: "boolean", Description: description}
}

// bounded describes an integer argument with a floor, a ceiling and an
// optional default.
//
// A fallback of zero declares NO default rather than a default of zero. Every
// bound here starts at one, so advertising a zero default would describe a
// value the same schema then refuses — and a client that fills in the
// defaults it was given would have its call rejected for obeying them.
func bounded(description string, minimum, maximum, fallback int64) Property {
	property := Property{
		Type:        "integer",
		Description: description,
		Minimum:     &minimum,
		Maximum:     &maximum,
	}
	if fallback > 0 {
		property.Default = fallback
	}
	return property
}

// invalidArgumentError marks an argument failure, which the server reports as
// a JSON-RPC invalid-params error rather than as a tool result.
//
// The distinction is the one the protocol draws: arguments that do not fit
// are a defect in the CALL, which the agent's runtime should correct, while
// anything that goes wrong reaching the cluster is an answer the model needs
// to read. Both would otherwise arrive as an ordinary error.
type invalidArgumentError struct {
	message string
}

func (e *invalidArgumentError) Error() string { return e.message }

// invalidArgument builds an argument failure.
func invalidArgument(format string, args ...any) error {
	return &invalidArgumentError{message: fmt.Sprintf(format, args...)}
}

// validate checks arguments against a tool's schema.
//
// Done once, centrally, rather than in each handler: every handler would
// otherwise repeat the same four checks and a new tool would repeat three of
// them. What handlers still do is the checking a schema cannot express — a
// namespace that must parse as a DNS label, two arguments that are each
// optional but must not both be given.
func validate(schema Schema, args map[string]any) error {
	for _, key := range schema.Required {
		value, present := args[key]
		if !present || value == nil {
			return invalidArgument("missing required argument %q", key)
		}
		if literal, isString := value.(string); isString && strings.TrimSpace(literal) == "" {
			return invalidArgument("argument %q must not be empty", key)
		}
	}

	for key, value := range args {
		property, known := schema.Properties[key]
		if !known {
			return invalidArgument("unknown argument %q (expected one of %s)", key, strings.Join(names(schema), ", "))
		}
		if value == nil {
			continue
		}
		if err := check(key, property, value); err != nil {
			return err
		}
	}

	return nil
}

// names lists a schema's accepted arguments, sorted so the message is stable.
func names(schema Schema) []string {
	accepted := make([]string, 0, len(schema.Properties))
	for key := range schema.Properties {
		accepted = append(accepted, key)
	}
	slices.Sort(accepted)
	return accepted
}

// check validates one value against its property.
func check(key string, property Property, value any) error {
	switch property.Type {
	case "string":
		literal, isString := value.(string)
		if !isString {
			return invalidArgument("argument %q must be a string", key)
		}
		if len(property.Enum) > 0 && !slices.Contains(property.Enum, literal) {
			return invalidArgument("argument %q must be one of %s", key, strings.Join(property.Enum, ", "))
		}

	case "boolean":
		if _, isBool := value.(bool); !isBool {
			return invalidArgument("argument %q must be true or false", key)
		}

	case "integer":
		// JSON has one number type, so an integer arrives as a float64 and
		// "5.5 lines of log" has to be refused here rather than truncated
		// into something the caller did not ask for.
		number, isNumber := value.(float64)
		if !isNumber {
			return invalidArgument("argument %q must be a number", key)
		}
		if number != float64(int64(number)) {
			return invalidArgument("argument %q must be a whole number", key)
		}
		whole := int64(number)
		if property.Minimum != nil && whole < *property.Minimum {
			return invalidArgument("argument %q must be at least %d", key, *property.Minimum)
		}
		if property.Maximum != nil && whole > *property.Maximum {
			return invalidArgument("argument %q must be at most %d", key, *property.Maximum)
		}
	}

	return nil
}

// Arguments are one tool call's validated arguments.
//
// Every accessor assumes validate has run, which is why none of them return
// an error: the type is already known to fit the schema, and a second round
// of type assertions in every handler would be noise around the one thing
// each handler is actually for.
type Arguments map[string]any

// String returns a trimmed string argument, empty when it was not given.
func (a Arguments) String(key string) string {
	literal, _ := a[key].(string)
	return strings.TrimSpace(literal)
}

// Int returns an integer argument, or fallback when it was not given.
func (a Arguments) Int(key string, fallback int64) int64 {
	number, given := a[key].(float64)
	if !given {
		return fallback
	}
	return int64(number)
}

// Bool returns a boolean argument, false when it was not given.
func (a Arguments) Bool(key string) bool {
	value, _ := a[key].(bool)
	return value
}
