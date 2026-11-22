package extraction

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/pinar-isler/BuildBid/validation/strictjson"
	"github.com/santhosh-tekuri/jsonschema/v6"
	schemaerror "github.com/santhosh-tekuri/jsonschema/v6/kind"
)

type Kind string

const (
	Quotation Kind = "quotation"
	TitleDeed Kind = "title_deed"
)

func (kind Kind) Supported() bool { return kind == Quotation || kind == TitleDeed }

const draft2020 = "https://json-schema.org/draft/2020-12/schema"

type Validators struct {
	byKind map[Kind]canonicalSchema
}

type canonicalSchema struct {
	compiled *jsonschema.Schema
	shape    *schemaNode
}

type schemaNode struct {
	typ        string
	stringEnum bool
	fields     []schemaField
	items      *schemaNode
}

type schemaField struct {
	name     string
	required bool
	node     *schemaNode
}

type offlineLoader struct{}

func (offlineLoader) Load(string) (any, error) {
	return nil, errors.New("external schema loading is disabled")
}

// LoadValidators reads the two canonical files from a filesystem rooted at
// Schemas/. Draft 2020-12 validation and format assertions are enabled.
func LoadValidators(schemas fs.FS) (*Validators, error) {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseLoader(offlineLoader{})
	compiler.RegisterFormat(&jsonschema.Format{Name: "date", Validate: checkDate})
	validators := &Validators{byKind: make(map[Kind]canonicalSchema)}

	for _, file := range []struct {
		kind Kind
		name string
	}{{Quotation, "Vendor_Quotation_Schema"}, {TitleDeed, "Title_deed_Schema"}} {
		data, err := fs.ReadFile(schemas, file.name)
		if err != nil {
			return nil, err
		}
		value, err := strictjson.Decode(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file.name, err)
		}
		doc, ok := value.(map[string]any)
		if !ok || doc["$schema"] != draft2020 {
			return nil, fmt.Errorf("%s: expected a Draft 2020-12 schema", file.name)
		}
		location := "file:///buildbid-schemas/" + file.name
		if err := compiler.AddResource(location, doc); err != nil {
			return nil, err
		}
		compiled, err := compiler.Compile(location)
		if err != nil {
			return nil, err
		}
		shape, err := projectionShape(json.RawMessage(data), doc)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file.name, err)
		}
		if shape.typ != "object" {
			return nil, fmt.Errorf("%s: the extraction schema must have an object root", file.name)
		}
		validators.byKind[file.kind] = canonicalSchema{compiled, shape}
	}
	return validators, nil
}

func checkDate(value any) error {
	date, isString := value.(string)
	if !isString {
		return nil
	}
	if len(date) == 10 && date[:4] != "0000" {
		if _, err := time.Parse("2006-01-02", date); err == nil {
			return nil
		}
	}
	return errors.New("expected an ISO calendar date")
}

// Read the projection shape from the canonical schema. Reject changes that
// could invalidate the projection or sign-only representation of numbers.
func projectionShape(raw json.RawMessage, doc map[string]any) (*schemaNode, error) {
	typ, _ := doc["type"].(string)
	allowed := []string{"$schema", "title", "description", "type"}
	switch typ {
	case "object":
		allowed = append(allowed, "properties", "required", "additionalProperties")
		if closed, ok := doc["additionalProperties"].(bool); !ok || closed {
			return nil, errors.New("expected a closed object schema")
		}
	case "array":
		allowed = append(allowed, "items")
	case "number":
		allowed = append(allowed, "minimum")
		if minimum, ok := doc["minimum"].(json.Number); !ok || numberSign(minimum) != 0 {
			return nil, errors.New("number projection requires a zero minimum")
		}
	case "string":
		allowed = append(allowed, "enum", "format")
		if format, exists := doc["format"]; exists && format != "date" {
			return nil, errors.New("unsupported string format in extraction schema")
		}
	case "boolean":
	default:
		return nil, fmt.Errorf("unsupported extraction schema type %q", typ)
	}
	for key := range doc {
		if !slices.Contains(allowed, key) {
			return nil, fmt.Errorf("unsupported extraction projection keyword %q", key)
		}
	}

	node := &schemaNode{typ: typ}
	if enum, ok := doc["enum"].([]any); ok {
		for _, value := range enum {
			if _, ok := value.(string); !ok {
				return nil, errors.New("extraction string enums must only contain strings")
			}
		}
		node.stringEnum = true
	}
	if typ != "object" && typ != "array" {
		return node, nil
	}
	var definition struct {
		Properties json.RawMessage `json:"properties"`
		Items      json.RawMessage `json:"items"`
		Required   []string        `json:"required"`
	}
	if err := json.Unmarshal(raw, &definition); err != nil {
		return nil, err
	}
	if typ == "array" {
		items, ok := doc["items"].(map[string]any)
		if !ok {
			return nil, errors.New("expected an item schema")
		}
		child, err := projectionShape(definition.Items, items)
		node.items = child
		return node, err
	}
	properties, ok := doc["properties"].(map[string]any)
	if !ok {
		return nil, errors.New("expected object properties")
	}
	// Read properties in their source order, which determines review-field
	// order in the Python reference.
	decoder := json.NewDecoder(bytes.NewReader(definition.Properties))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name := key.(string)
		child, ok := properties[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected a schema for %q", name)
		}
		var childRaw json.RawMessage
		if err := decoder.Decode(&childRaw); err != nil {
			return nil, err
		}
		fieldNode, err := projectionShape(childRaw, child)
		if err != nil {
			return nil, err
		}
		node.fields = append(node.fields, schemaField{name, slices.Contains(definition.Required, name), fieldNode})
	}
	for _, required := range definition.Required {
		if _, ok := properties[required]; !ok {
			return nil, fmt.Errorf("missing property schema for %q", required)
		}
	}
	return node, nil
}

// ValidateCanonical checks the original value without applying review rules.
// payload uses the data model returned by strictjson.Decode.
func (v *Validators) ValidateCanonical(kind Kind, payload any) ([]ExtractionError, error) {
	canonical, ok := v.byKind[kind]
	if !ok {
		return nil, errors.New("unsupported document kind")
	}
	return canonical.violations(payload, false), nil
}

func (s canonicalSchema) violations(payload any, ignoreRequired bool) []ExtractionError {
	err := s.compiled.Validate(s.shape.validationValue(payload))
	if err == nil {
		return nil
	}
	violations := []ExtractionError{}
	var collect func(*jsonschema.ValidationError)
	collect = func(err *jsonschema.ValidationError) {
		switch err.ErrorKind.(type) {
		case *schemaerror.Schema, *schemaerror.Group:
			for _, cause := range err.Causes {
				collect(cause)
			}
			return
		}
		pointer := ""
		for _, part := range err.InstanceLocation {
			pointer = joinPointer(pointer, part)
		}
		add := func(rule string) {
			violations = append(violations, ExtractionError{
				Code: "schema_violation", Pointer: pointer,
				Message: fmt.Sprintf("Canonical schema rule '%s' failed.", rule),
			})
		}
		switch rule := err.ErrorKind.(type) {
		case *schemaerror.Required:
			if !ignoreRequired {
				for range rule.Missing {
					add("required")
				}
			}
		case *schemaerror.Type:
			add("type")
			// The reference reports enum and type independently. The Go
			// validator stops at type, but a non-string cannot satisfy a
			// string-only enum.
			at := s.shape.at(err.InstanceLocation)
			if at != nil && at.stringEnum {
				add("enum")
			}
		default:
			keyword := err.ErrorKind.KeywordPath()
			if len(keyword) == 0 {
				add("type")
			} else {
				add(keyword[0])
			}
		}
	}
	collect(err.(*jsonschema.ValidationError))
	slices.SortFunc(violations, func(left, right ExtractionError) int {
		if order := strings.Compare(left.Pointer, right.Pointer); order != 0 {
			return order
		}
		return strings.Compare(left.Message, right.Message)
	})
	return violations
}

func (n *schemaNode) at(path []string) *schemaNode {
	if n == nil || len(path) == 0 {
		return n
	}
	if n.typ == "array" {
		return n.items.at(path[1:])
	}
	for _, field := range n.fields {
		if field.name == path[0] {
			return field.node.at(path[1:])
		}
	}
	return nil
}

func (n *schemaNode) validationValue(payload any) any {
	switch n.typ {
	case "number":
		if number, ok := payload.(json.Number); ok {
			// These schemas only constrain numbers by type and minimum zero.
			// Passing the sign avoids expanding huge exponents in big.Rat,
			// while preserving the original exact number in the result.
			return numberSign(number)
		}
	case "array":
		if values, ok := payload.([]any); ok {
			checked := make([]any, len(values))
			for i, value := range values {
				checked[i] = n.items.validationValue(value)
			}
			return checked
		}
	case "object":
		if object, ok := payload.(map[string]any); ok {
			checked := maps.Clone(object)
			for _, field := range n.fields {
				if value, exists := object[field.name]; exists {
					checked[field.name] = field.node.validationValue(value)
				}
			}
			return checked
		}
	}
	return payload
}

func numberSign(number json.Number) int {
	for _, char := range number {
		if char == 'e' || char == 'E' {
			break
		}
		if char >= '1' && char <= '9' {
			if number[0] == '-' {
				return -1
			}
			return 1
		}
	}
	return 0
}
