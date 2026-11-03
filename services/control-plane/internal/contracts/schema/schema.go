// Package schema validates documents against the embedded JSON Schemas. It is kept separate from
// package contracts so that the pure flow engine never depends on a schema library.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane/internal/contracts"
)

var (
	schemaMu    sync.Mutex
	schemaCache = map[string]*jsonschema.Schema{}
)

// Schema compiles (once) and returns an embedded JSON Schema.
func Schema(rel string) (*jsonschema.Schema, error) {
	schemaMu.Lock()
	defer schemaMu.Unlock()
	if s, ok := schemaCache[rel]; ok {
		return s, nil
	}
	raw, err := contracts.Read(rel)
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse schema %s: %w", rel, err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	url := "embedded:///" + rel
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	s, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("compile schema %s: %w", rel, err)
	}
	schemaCache[rel] = s
	return s, nil
}

// ValidateJSON validates a JSON document against an embedded schema.
func ValidateJSON(rel string, raw []byte) error {
	s, err := Schema(rel)
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("not JSON: %w", err)
	}
	return s.Validate(doc)
}

// ValidateValue marshals v and validates it against an embedded schema.
func ValidateValue(rel string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return ValidateJSON(rel, raw)
}
