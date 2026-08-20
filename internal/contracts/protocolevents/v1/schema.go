package v1

import (
	"bytes"
	_ "embed"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const SchemaURL = "https://loramapr.dev/contracts/protocol-events/v1/normalized-event.schema.json"

//go:embed normalized-event.schema.json
var schemaDocument []byte

var (
	compileOnce sync.Once
	compiled    *jsonschema.Schema
	compileErr  error
)

// Validate checks a normalized event against the single vendored v1 contract.
// Unknown v1 minor fields remain allowed by that authoritative schema.
func Validate(document []byte) error {
	compileOnce.Do(func() {
		schemaValue, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaDocument))
		if err != nil {
			compileErr = fmt.Errorf("decode normalized event schema: %w", err)
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.AssertFormat()
		if err := compiler.AddResource(SchemaURL, schemaValue); err != nil {
			compileErr = fmt.Errorf("register normalized event schema: %w", err)
			return
		}
		compiled, compileErr = compiler.Compile(SchemaURL)
	})
	if compileErr != nil {
		return compileErr
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(document))
	if err != nil {
		return fmt.Errorf("decode normalized event: %w", err)
	}
	if err := compiled.Validate(instance); err != nil {
		return fmt.Errorf("validate normalized event: %w", err)
	}
	return nil
}
