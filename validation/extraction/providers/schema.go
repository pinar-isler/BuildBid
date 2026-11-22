package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"slices"

	"github.com/pinar-isler/BuildBid/validation/extraction"
	"github.com/pinar-isler/BuildBid/validation/strictjson"
)

type requestSchema struct {
	title string
	body  json.RawMessage
}

func (a adapter) requestSchema(kind extraction.Kind) (requestSchema, error) {
	var filename string
	switch kind {
	case extraction.Quotation:
		filename = "Vendor_Quotation_Schema"
	case extraction.TitleDeed:
		filename = "Title_deed_Schema"
	default:
		return requestSchema{}, providerError("Use quotation or title_deed as the document kind.")
	}
	data, err := fs.ReadFile(a.schemas, filename)
	if err == nil {
		var schema requestSchema
		schema, err = projectRequestSchema(data)
		if err == nil {
			return schema, nil
		}
	}
	return requestSchema{}, providerError("The local extraction request schema could not be loaded.")
}

func projectRequestSchema(data []byte) (requestSchema, error) {
	if _, err := strictjson.Decode(data); err != nil {
		return requestSchema{}, err
	}
	root, err := readMembers(data)
	if err != nil {
		return requestSchema{}, err
	}
	title := root.stringValue("title")
	if root.stringValue("type") != "object" || title == "" || root.value("$schema") == nil {
		return requestSchema{}, errors.New("expected a named canonical object schema")
	}
	root = slices.DeleteFunc(root, func(member schemaMember) bool { return member.name == "$schema" })
	body, err := allowUnavailable(root, false)
	return requestSchema{title, body}, err
}

// Project only property values. Array items and the root retain their types,
// and required keys, enums and other constraints stay in the request schema.
func allowUnavailable(schema schemaMembers, nullable bool) (json.RawMessage, error) {
	typ := schema.stringValue("type")
	switch typ {
	case "object":
		properties, err := readMembers(schema.value("properties"))
		if err != nil {
			return nil, err
		}
		for i := range properties {
			member, err := readMembers(properties[i].value)
			if err != nil {
				return nil, err
			}
			properties[i].value, err = allowUnavailable(member, true)
			if err != nil {
				return nil, err
			}
		}
		schema.setValue("properties", properties.encode())
	case "array":
		items, err := readMembers(schema.value("items"))
		if err != nil {
			return nil, err
		}
		projected, err := allowUnavailable(items, false)
		if err != nil {
			return nil, err
		}
		schema.setValue("items", projected)
	case "string", "number", "boolean":
	default:
		return nil, errors.New("unsupported provider request schema type")
	}
	if nullable {
		if schema.value("enum") != nil {
			return json.Marshal(map[string][]json.RawMessage{
				"anyOf": {schema.encode(), json.RawMessage(`{"type":"null"}`)},
			})
		}
		value, err := json.Marshal([]string{typ, "null"})
		if err != nil {
			return nil, err
		}
		schema.setValue("type", value)
	}
	return schema.encode(), nil
}

// Use the canonical property order in the request so it still defines the
// order for providers that generate structured output in schema order.
type schemaMember struct {
	name  string
	value json.RawMessage
}

type schemaMembers []schemaMember

func readMembers(data []byte) (schemaMembers, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, errors.New("expected a JSON schema object")
	}
	var members schemaMembers
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		member := schemaMember{name: key.(string)}
		if err := decoder.Decode(&member.value); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	_, err := decoder.Token()
	return members, err
}

func (m schemaMembers) value(name string) json.RawMessage {
	for _, member := range m {
		if member.name == name {
			return member.value
		}
	}
	return nil
}

func (m schemaMembers) stringValue(name string) string {
	var value string
	_ = json.Unmarshal(m.value(name), &value)
	return value
}

func (m schemaMembers) setValue(name string, value json.RawMessage) {
	for i := range m {
		if m[i].name == name {
			m[i].value = value
			return
		}
	}
}

func (m schemaMembers) encode() json.RawMessage {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, member := range m {
		if i != 0 {
			out.WriteByte(',')
		}
		name, _ := json.Marshal(member.name)
		out.Write(name)
		out.WriteByte(':')
		out.Write(member.value)
	}
	out.WriteByte('}')
	return out.Bytes()
}
