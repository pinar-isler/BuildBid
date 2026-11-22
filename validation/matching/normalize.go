package matching

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	wordChars = `\p{L}\p{N}_`
	// Python's Unicode whitespace includes these four C0 separators.
	space = `[\t-\r\x{1c}-\x{1f}\x{85}\p{Z}]`
)

var (
	folder            = cases.Fold()
	textReplacements  = strings.NewReplacer("ı", "i", "×", "x", "⌀", "ø", "∅", "ø", "φ", "ø")
	unitTokens        = regexp.MustCompile(`[\p{L}\p{N}]+`)
	descriptionTokens = regexp.MustCompile(`[\p{L}\p{N}]+(?:[./]\p{Nd}+)*`)
	whitespace        = regexp.MustCompile(space + "+")
	unitAliases       = makeUnitAliases()
)

func makeUnitAliases() map[string]string {
	aliases := map[string][]string{
		"m":    {"metre", "meter", "metres", "meters"},
		"m2":   {"sqm", "sq m", "metrekare", "square metre", "square metres", "square meter", "square meters"},
		"m3":   {"cbm", "cu m", "metrekup", "cubic metre", "cubic metres", "cubic meter", "cubic meters"},
		"kg":   {"kilogram", "kilograms", "kgs"},
		"ton":  {"t", "tonne", "tonnes"},
		"pcs":  {"adet", "ad", "pc", "piece", "pieces"},
		"h":    {"saat", "sa", "hr", "hrs", "hour", "hours"},
		"trip": {"sefer", "trips"},
		"week": {"hafta", "weeks"},
		"day":  {"gun", "days"},
		"job":  {"is", "jobs"},
		"l":    {"litre", "liter", "litres", "liters"},
	}
	units := make(map[string]string)
	for unit, spellings := range aliases {
		units[unit] = unit
		for _, spelling := range spellings {
			units[spelling] = unit
		}
	}
	return units
}

func fold(text string) string {
	var normalized strings.Builder
	for _, r := range folder.String(text) {
		if r <= unicode.MaxASCII {
			normalized.WriteByte(byte(r))
			continue
		}
		// Combining marks with nonzero classes are discarded, so their
		// ordering is irrelevant. Decompose individual runes to avoid
		// inserting stream-safe joiners into long runs of combining marks.
		for _, part := range norm.NFKD.String(string(r)) {
			if norm.NFKD.PropertiesString(string(part)).CCC() == 0 {
				normalized.WriteRune(part)
			}
		}
	}
	return textReplacements.Replace(normalized.String())
}

func isSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= '\x1c' && r <= '\x1f')
}

func isWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// NormalizeUnit resolves spelling aliases within a unit without converting quantities.
func NormalizeUnit(unit string) string {
	normalized := strings.Join(unitTokens.FindAllString(fold(unit), -1), " ")
	if normalized == "m 2" || normalized == "m 3" {
		normalized = strings.ReplaceAll(normalized, " ", "")
	}
	if canonical, ok := unitAliases[normalized]; ok {
		return canonical
	}
	return normalized
}

type specSet map[string]struct{}

type description struct {
	text, sortedText string
	specs            map[string]specSet
}

func normalizeDescription(text string) description {
	text = fold(text)
	specs := make(map[string]specSet)
	for _, spec := range specifications {
		text = spec.pattern.replace(text, func(match match) string {
			if specs[spec.name] == nil {
				specs[spec.name] = make(specSet)
			}
			specs[spec.name][spec.value(match)] = struct{}{}
			return " "
		})
	}
	for _, alias := range descriptionAliases {
		text = alias.pattern.replace(text, func(match) string { return alias.term })
	}
	tokens := descriptionTokens.FindAllString(text, -1)
	original := strings.Join(tokens, " ")
	slices.Sort(tokens)
	return description{original, strings.Join(tokens, " "), specs}
}

func compareSpecs(quoted, boq description) ([]string, bool) {
	names := make(map[string]specSet, len(quoted.specs)+len(boq.specs))
	maps.Copy(names, quoted.specs)
	maps.Copy(names, boq.specs)
	unconfirmed := []string{}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		left, right := quoted.specs[name], boq.specs[name]
		if maps.Equal(left, right) {
			continue
		}
		if !subset(left, right) && !subset(right, left) &&
			(name != "concrete grade" || !compatibleGrades(left, right)) {
			return nil, false
		}
		unconfirmed = append(unconfirmed, name)
	}
	return unconfirmed, true
}

func subset(left, right specSet) bool {
	for value := range left {
		if _, exists := right[value]; !exists {
			return false
		}
	}
	return true
}

func compatibleGrades(left, right specSet) bool {
	allAgree := func(left, right specSet) bool {
		for a := range left {
			found := false
			for b := range right {
				if a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/") {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	return allAgree(left, right) && allAgree(right, left)
}
