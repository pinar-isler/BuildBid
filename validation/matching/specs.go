package matching

import (
	"math/big"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	number     = `\p{Nd}+(?:[.,]\p{Nd}+)?`
	lengthUnit = `mm|cm|m`

	wordEnd = `(?:$|[^` + wordChars + `])`
	// This also rejects a decimal continuation after an otherwise valid size.
	quantityEnd = `(?:$|[^` + wordChars + `.,]|[.,](?:$|[^\p{Nd}]))`
	measurement = `(` + number + `)` + space + `*(` + lengthUnit + `)`
)

type match []string

type tokenPattern struct {
	re              *regexp.Regexp
	alternative     *regexp.Regexp
	rejectDecimal   bool
	rejectGradeTail bool
}

// RE2 has no lookaround. Match the right boundary as part of the expression
// so optional units can still backtrack, but replace only the captured token.
// Check the left boundary against the original text rather than consuming it.
func token(body, end string, rejectDecimal bool) tokenPattern {
	return tokenPattern{re: regexp.MustCompile(`(` + body + `)` + end), rejectDecimal: rejectDecimal}
}

func (p tokenPattern) replace(text string, replacement func(match) string) string {
	var output strings.Builder
	written := 0
	for search := 0; search < len(text); {
		indices := p.re.FindStringSubmatchIndex(text[search:])
		if p.alternative != nil {
			other := p.alternative.FindStringSubmatchIndex(text[search:])
			if other != nil && (indices == nil || other[0] < indices[0]) {
				indices = other
			}
		}
		if indices == nil {
			break
		}
		start, end := search+indices[2], search+indices[3]
		if !p.accepts(text, start, end) {
			_, size := utf8.DecodeRuneInString(text[start:])
			search = start + size
			continue
		}
		values := make(match, len(indices)/2-1)
		for i := range values {
			groupStart, groupEnd := indices[2*i+2], indices[2*i+3]
			if groupStart >= 0 {
				values[i] = text[search+groupStart : search+groupEnd]
			}
		}
		output.WriteString(text[written:start])
		output.WriteString(replacement(values))
		written, search = end, end
	}
	if written == 0 {
		return text
	}
	output.WriteString(text[written:])
	return output.String()
}

func (p tokenPattern) accepts(text string, start, end int) bool {
	before, width := utf8.DecodeLastRuneInString(text[:start])
	if isWord(before) {
		return false
	}
	first, _ := utf8.DecodeRuneInString(text[start:])
	if p.rejectDecimal && unicode.IsDigit(first) && (before == '.' || before == ',') {
		previous, _ := utf8.DecodeLastRuneInString(text[:start-width])
		if unicode.IsDigit(previous) {
			return false
		}
	}
	if p.rejectGradeTail {
		after := strings.TrimLeftFunc(text[end:], isSpace)
		if after, slash := strings.CutPrefix(after, "/"); slash {
			next, _ := utf8.DecodeRuneInString(strings.TrimLeftFunc(after, isSpace))
			return !unicode.IsDigit(next)
		}
	}
	return true
}

type specification struct {
	name    string
	pattern tokenPattern
	value   func(match) string
}

var sizePart = regexp.MustCompile(measurement + "?")

var specifications = []specification{
	{"nominal diameter", token(`dn(?:`+space+`|-)*(\p{Nd}+)(?:`+space+`*mm)?`, quantityEnd, false),
		func(m match) string { return integer(m[1]) }},
	{"diameter", diameterPattern(), func(m match) string {
		// With no explicit unit a diameter value is already in millimetres.
		return decimal(m[1], m[2])
	}},
	{"compound dimensions", token(`(?:`+number+space+`*(?:`+lengthUnit+`)?`+space+`*x`+space+`*){1,2}`+
		number+`(?:`+space+`*(?:`+lengthUnit+`))?`, quantityEnd, true), compoundSize},
	namedMeasurement("width", `width|wide|genislik|genisliginde`),
	namedMeasurement("height", `height|high|yukseklik|yuksekliginde`),
	namedMeasurement("thickness", `thickness|thick|kalinlik|kalinliginda`),
	{"measurement", token(measurement, wordEnd, true),
		func(m match) string { return decimal(m[1], m[2]) }},
	{"concrete grade", concreteGradePattern(), func(m match) string {
		if m[2] != "" {
			return integer(m[1]) + "/" + integer(m[2])
		}
		return integer(m[1])
	}},
	{"steel grade", token(`(b`+space+`*\p{Nd}{3}`+space+`*[abc]|s`+space+`*\p{Nd}{3}(?:`+
		space+`*(?:jr|j0|j2|k2))?)`, wordEnd, false),
		func(m match) string { return whitespace.ReplaceAllString(m[1], "") }},
	{"fire rating", token(`(rei|ei)`+space+`*[- ]?`+space+`*(\p{Nd}{2,3})`, wordEnd, false),
		func(m match) string { return m[1] + integer(m[2]) }},
	{"application", token(`(?:(ic(?:`+space+`|-)+(?:cephe|mekan)|interior|indoor)`+
		`|(dis(?:`+space+`|-)+(?:cephe|mekan)|exterior|outdoor)|(cati|roof)|(duvar|wall))(?:`+
		space+`+icin)?`, wordEnd, false), application},
}

func concreteGradePattern() tokenPattern {
	pattern := token(`c(?:`+space+`|-)*(\p{Nd}{2,3})(?:`+space+`*/`+space+`*(\p{Nd}{2,3}))?`, quantityEnd, false)
	pattern.rejectGradeTail = true
	return pattern
}

func diameterPattern() tokenPattern {
	prefix := token(`(?:ø|(?:cap|diameter|dia)`+space+`*[:=]?)`+space+`*(`+number+
		`)(?:`+space+`*(`+lengthUnit+`))?`, quantityEnd, true)
	suffix := token(measurement+space+`+(?:cap|diameter)`, wordEnd, true)
	prefix.alternative = suffix.re
	return prefix
}

func namedMeasurement(name, terms string) specification {
	terms = `(?:` + terms + `)`
	// The prefix term ends at a word boundary, so it requires whitespace or
	// punctuation before the numeric value.
	separator := `(?:` + space + `+[:=]?|[:=])` + space + `*`
	return specification{
		name, token(`(?:`+terms+separator+measurement+`|`+measurement+space+`+`+terms+`)`, wordEnd, true),
		func(m match) string {
			if m[1] != "" {
				return decimal(m[1], m[2])
			}
			return decimal(m[3], m[4])
		},
	}
}

var descriptionAliases = []struct {
	pattern tokenPattern
	term    string
}{
	{token(`(?:hazir`+space+`+beton|ready(?:`+space+`|-)+mix(?:ed)?`+space+`+concrete)`, wordEnd, false), "hazirbeton"},
	{token(`(?:pompali|pompa`+space+`+ile|pumped|pump(?:`+space+`|-)+placed)`, wordEnd, false), "pompali"},
	{token(`(?:drenaj`+space+`+boru(?:su)?|drain(?:age)?`+space+`+pipe)`, wordEnd, false), "drenajborusu"},
	{token(`mineral`+space+`+(?:yun|wool)`, wordEnd, false), "mineralyun"},
	{token(`(?:yalitim(?:i)?|insulation)`, wordEnd, false), "yalitim"},
	{token(`(?:yangin`+space+`+kapisi|fire(?:(?:`+space+`|-)+rated)?`+space+`+door)`, wordEnd, false), "yanginkapisi"},
	{token(`(?:donati`+space+`+celigi|rebar|reinforcing`+space+`+(?:steel|bar))`, wordEnd, false), "donaticeligi"},
}

func application(m match) string {
	for i, name := range [...]string{"interior", "exterior", "roof", "wall"} {
		if m[i+1] != "" {
			return name
		}
	}
	return ""
}

func compoundSize(m match) string {
	parts := sizePart.FindAllStringSubmatch(m[0], -1)
	units := make(map[string]struct{})
	for _, part := range parts {
		if part[2] != "" {
			units[part[2]] = struct{}{}
		}
	}
	defaultUnit := ""
	if len(units) == 1 {
		for unit := range units {
			defaultUnit = unit
		}
	}
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		unit := part[2]
		if unit == "" {
			unit = defaultUnit
		}
		value := decimal(part[1], unit)
		if unit != "" {
			value += "mm"
		}
		values = append(values, value)
	}
	return strings.Join(values, "x")
}

func asciiDigit(r rune) rune {
	if r <= unicode.MaxASCII || !unicode.IsDigit(r) {
		return r
	}
	// Unicode Nd lists decimal sets in blocks of ten, starting at zero.
	for _, table := range unicode.Digit.R16 {
		if rune(table.Lo) <= r && r <= rune(table.Hi) && (r-rune(table.Lo))%rune(table.Stride) == 0 {
			return '0' + (r-rune(table.Lo))/rune(table.Stride)%10
		}
	}
	for _, table := range unicode.Digit.R32 {
		if rune(table.Lo) <= r && r <= rune(table.Hi) && (r-rune(table.Lo))%rune(table.Stride) == 0 {
			return '0' + (r-rune(table.Lo))/rune(table.Stride)%10
		}
	}
	return r
}

func integer(value string) string {
	digits := strings.TrimLeft(strings.Map(asciiDigit, value), "0")
	if digits == "" {
		return "0"
	}
	return digits
}

// decimal uses base-ten arithmetic for unit conversion and Python Decimal's
// default 28 significant digits with half-even rounding.
func decimal(value, unit string) string {
	value = strings.ReplaceAll(strings.Map(asciiDigit, value), ",", ".")
	whole, fraction, _ := strings.Cut(value, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return "0"
	}
	exponent := -len(fraction)
	switch unit {
	case "cm":
		exponent++
	case "m":
		exponent += 3
	}
	const precision = 28
	if len(digits) > precision {
		exponent += len(digits) - precision
		kept, discarded := digits[:precision], digits[precision:]
		digits = kept
		if discarded[0] > '5' || (discarded[0] == '5' &&
			(strings.Trim(discarded[1:], "0") != "" || (kept[precision-1]-'0')%2 == 1)) {
			rounded, _ := new(big.Int).SetString(kept, 10)
			digits = rounded.Add(rounded, big.NewInt(1)).String()
		}
	}
	trimmed := strings.TrimRight(digits, "0")
	exponent += len(digits) - len(trimmed)
	if exponent >= 0 {
		return trimmed + strings.Repeat("0", exponent)
	}
	point := len(trimmed) + exponent
	if point <= 0 {
		return "0." + strings.Repeat("0", -point) + trimmed
	}
	return trimmed[:point] + "." + trimmed[point:]
}
