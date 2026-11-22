package strictjson

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestStrictJSONRejectsInvalidInput(t *testing.T) {
	cases := map[string]string{
		"duplicate root":       `{"name": 1, "name": 2}`,
		"duplicate in array":   `[{"item":{"key":1,"\u006bey":2}}]`,
		"duplicate emoji":      `{"😀":0,"\uD83D\uDE00":1}`,
		"nonfinite NaN":        `{"x":NaN}`,
		"positive infinity":    `{"x":Infinity}`,
		"negative infinity":    `{"x":-Infinity}`,
		"trailing value":       `{} []`,
		"trailing garbage":     `1!`,
		"wrapped content":      "\x60\x60\x60json\n{}\n\x60\x60\x60",
		"incomplete object":    `{"x": }`,
		"trailing comma":       `{"x":[1,]}`,
		"leading zero":         `{"x":01}`,
		"empty exponent":       `1e+`,
		"plus sign":            `+1`,
		"escaped high":         `{"x":"\ud800"}`,
		"escaped low key":      `{"\udc00":0}`,
		"mismatched pair":      `["\ud800\u0061"]`,
		"reversed pair":        `["\udc00\ud800"]`,
		"bad hexadecimal":      `["\uG012"]`,
		"invalid UTF-8":        "{\"x\":\"\xff\"}",
		"UTF-8 surrogate code": "\"\xed\xa0\x80\"",
		"UTF-8 BOM":            "\ufeff{}",
		"excess nesting":       strings.Repeat("[", maxDepth+2) + "0" + strings.Repeat("]", maxDepth+2),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if value, err := Decode([]byte(data)); err == nil {
				t.Fatalf("accepted invalid JSON as %v", value)
			}
		})
	}
}

func TestStrictJSONRetainsNumericLexemesAndValueStates(t *testing.T) {
	input := `{"zero":0,"no":false,"unset":null,"fraction":1250.50,"exact":0.123456789012345678901,"big":9007199254740993,"negativeZero":-0,"exponent":1.20e+003,"text":"İşçilik \uD83D\uDE00 \uFFFD \\ud800"}`
	value, err := Decode([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"zero": json.Number("0"), "no": false, "unset": nil,
		"fraction": json.Number("1250.50"), "exact": json.Number("0.123456789012345678901"),
		"big": json.Number("9007199254740993"), "negativeZero": json.Number("-0"),
		"exponent": json.Number("1.20e+003"), "text": "İşçilik 😀 � \\ud800",
	}
	if !reflect.DeepEqual(value, want) {
		t.Fatalf("decoded value = %#v, want %#v", value, want)
	}
	if _, exists := value.(map[string]any)["absent"]; exists {
		t.Fatal("decoder populated an absent property")
	}
	encoded, err := Encode(value)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Decode(encoded)
	if err != nil || !reflect.DeepEqual(again, value) {
		t.Fatalf("round trip changed values or lexemes: %s (%v)", encoded, err)
	}
}

func TestStrictEncodingRejectsInvalidValues(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	for _, value := range []any{
		json.Number("NaN"), json.Number("Infinity"), json.Number("01"), json.Number(""),
		json.Number("1 2"), math.Inf(-1), math.NaN(), "\xff",
		map[string]any{"\xff": nil}, []any{map[string]any{"x": "\xff"}}, cyclic,
	} {
		if _, err := Encode(value); err == nil {
			t.Fatal("encoded an invalid value")
		}
	}
}
