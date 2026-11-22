// Package strictjson reads JSON without losing number spelling or accepting
// duplicate keys and malformed Unicode.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const maxDepth = 1000

var (
	errUnicode = errors.New("JSON strings must contain valid Unicode")
	errDepth   = errors.New("JSON nesting limit exceeded")
)

// Decode returns ordinary JSON values with numbers represented as json.Number.
func Decode(data []byte) (any, error) {
	if !utf8.Valid(data) || !validStringEscapes(data) {
		return nil, errUnicode
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := readValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing data after the JSON value")
	}
	return value, nil
}

func readValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > maxDepth {
		return nil, errDepth
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := make(map[string]any)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("JSON object key must be a string")
			}
			if _, duplicate := object[name]; duplicate {
				return nil, errors.New("duplicate JSON key")
			}
			value, err := readValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		_, err := decoder.Token()
		return object, err
	case json.Delim('['):
		array := []any{}
		for decoder.More() {
			value, err := readValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err := decoder.Token()
		return array, err
	case json.Delim('}'), json.Delim(']'):
		return nil, errors.New("unexpected JSON delimiter")
	default:
		return token, nil
	}
}

// The standard tokenizer replaces lone escaped surrogates with U+FFFD.
// Check those escapes first, leaving all other syntax checks to the decoder.
func validStringEscapes(data []byte) bool {
	inString := false
	for i := 0; i < len(data); i++ {
		switch {
		case data[i] == '"':
			inString = !inString
		case inString && data[i] == '\\':
			i++
			if i >= len(data) {
				return false
			}
			if data[i] != 'u' {
				continue
			}
			code, ok := hex4(data[i+1:])
			if !ok || (code >= 0xdc00 && code <= 0xdfff) {
				return false
			}
			i += 4
			if code >= 0xd800 && code <= 0xdbff {
				next := i + 1
				if len(data)-next < 6 || data[next] != '\\' || data[next+1] != 'u' {
					return false
				}
				low, ok := hex4(data[next+2:])
				if !ok || low < 0xdc00 || low > 0xdfff {
					return false
				}
				i += 6
			}
		}
	}
	return true
}

func hex4(data []byte) (uint16, bool) {
	if len(data) < 4 {
		return 0, false
	}
	var value uint16
	for _, digit := range data[:4] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value += uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value += uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value += uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

// Encode accepts the same JSON data model as Decode. Use json.Number for
// numeric values to avoid a conversion through floating point.
func Encode(value any) ([]byte, error) {
	if err := checkValue(value, 0); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func checkValue(value any, depth int) error {
	if depth > maxDepth {
		return errDepth
	}
	switch value := value.(type) {
	case nil, bool:
	case json.Number:
		if value == "" {
			return errors.New("empty JSON number")
		}
		// json.Marshal checks nonempty Number values against the number grammar.
	case string:
		if !utf8.ValidString(value) {
			return errUnicode
		}
	case []any:
		for _, item := range value {
			if err := checkValue(item, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range value {
			if !utf8.ValidString(key) {
				return errUnicode
			}
			if err := checkValue(item, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("value is outside the strict JSON data model")
	}
	return nil
}
