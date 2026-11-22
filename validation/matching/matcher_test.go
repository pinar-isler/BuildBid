package matching

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

func boq(code, text, unit string) BOQItem {
	return BOQItem{ItemCode: code, Description: text, UnitOfMeasure: unit}
}

func quoted(text, unit string) QuotationItem {
	return QuotationItem{RawDescription: text, UnitOfMeasure: unit}
}

func newMatcher(t *testing.T, rows ...BOQItem) *BOQMatcher {
	t.Helper()
	matcher, err := NewBOQMatcher(rows)
	if err != nil {
		t.Fatal(err)
	}
	return matcher
}

func checkDecision(t *testing.T, result Result, status Status, code string) {
	t.Helper()
	if result.Status != status || (code == "" && result.ItemCode != nil) ||
		(code != "" && (result.ItemCode == nil || *result.ItemCode != code)) {
		t.Fatalf("want %s %q, got %+v", status, code, result)
	}
}

func checkCandidates(t *testing.T, result Result, want ...string) {
	t.Helper()
	codes := make([]string, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		codes = append(codes, candidate.ItemCode)
	}
	if !slices.Equal(codes, want) {
		t.Fatalf("candidates = %v, want %v", codes, want)
	}
}

func checkUnconfirmed(t *testing.T, candidate Candidate, want ...string) {
	t.Helper()
	if !slices.Equal(candidate.UnconfirmedSpecs, want) {
		t.Fatalf("unconfirmed specs = %v, want %v", candidate.UnconfirmedSpecs, want)
	}
}

func TestLevenshteinDistance(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"kitten", "sitting", 3}, {"sitting", "kitten", 3},
		{"", "örnek", 5}, {"İstanbul", "İstanbul", 0},
	} {
		t.Run(test.left+"/"+test.right, func(t *testing.T) {
			if got := LevenshteinDistance(test.left, test.right); got != test.want {
				t.Fatalf("distance = %d, want %d", got, test.want)
			}
		})
	}
}

func TestNormalizesTurkishCaseAccentsAndWordOrder(t *testing.T) {
	text := norm.NFD.String("İÇ-CEPHE ÇELİK KİRİŞ BOYASI")
	m := newMatcher(t, boq("P1", text, "m²"), boq("P2", "Dış cephe çelik kiriş boyası", "m2"))
	result := m.MatchItem(quoted("boyasi / kiris celik  ic cephe", "METREKARE"))
	checkDecision(t, result, Matched, "P1")
	checkCandidates(t, result, "P1")
	if got := result.Candidates[0].Score; got != 1 {
		t.Fatalf("score = %v, want 1", got)
	}
}

func TestEditDistanceMatchesASmallSpellingError(t *testing.T) {
	m := newMatcher(t, boq("A", "Çelik ankraj plakası", "pcs"))
	result := m.MatchItem(quoted("Çelik ankaraj plakası", "adet"))
	checkDecision(t, result, Matched, "A")
	checkCandidates(t, result, "A")
	if got := result.Candidates[0].Score; got >= 1 {
		t.Fatalf("score = %v, want less than 1", got)
	}
}

func TestUnitAliasesAndCrossUnitRejection(t *testing.T) {
	for _, test := range []struct{ alias, canonical string }{
		{"m³", "m3"}, {"M^2", "m2"}, {"metreküp", "m3"}, {"adet", "pcs"},
		{"SAAT", "h"}, {"sefer", "trip"}, {"weeks", "week"}, {"tonne", "ton"},
	} {
		t.Run(test.alias, func(t *testing.T) {
			if got := NormalizeUnit(test.alias); got != test.canonical {
				t.Fatalf("unit = %q, want %q", got, test.canonical)
			}
		})
	}
	m := newMatcher(t, boq("MASS", "Nervürlü donatı çeliği", "kg"))
	result := m.MatchItem(quoted("Nervürlü donatı çeliği", "ton"))
	checkDecision(t, result, Unmatched, "")
	checkCandidates(t, result)
}

func TestDiameterAliasesExcludeConflictingValues(t *testing.T) {
	m := newMatcher(t, boq("D12", "Ø12, nervürlü çelik çubuk", "kg"),
		boq("D16", "Ø16 nervürlü çelik çubuk", "kg"))
	result := m.MatchItem(quoted("1,2 cm çap nervürlü çelik çubuk", "kilogram"))
	checkDecision(t, result, Matched, "D12")
	checkCandidates(t, result, "D12")
}

func TestNominalAndMeasuredDiameterNeedDifferentEvidence(t *testing.T) {
	const text = "galvanizli basınçlı yangın tesisatı borusu"
	m := newMatcher(t, boq("DN80", "DN-80 "+text, "m"))
	checkDecision(t, m.MatchItem(quoted(text+" DN 80mm", "metre")), Matched, "DN80")
	checkDecision(t, m.MatchItem(quoted("DN100 "+text, "m")), Unmatched, "")
	result := m.MatchItem(quoted("Ø80 "+text, "m"))
	checkDecision(t, result, Ambiguous, "")
	checkCandidates(t, result, "DN80")
	checkUnconfirmed(t, result.Candidates[0], "diameter", "nominal diameter")
}

func TestMeasuredAndCompoundDimensionsUseConsistentUnits(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		m := newMatcher(t, boq("75", "Yalıtım levhası 75 mm", "m2"), boq("90", "Yalıtım levhası 90 mm", "m2"))
		result := m.MatchItem(quoted("Yalıtım levhası 0,075 m", "m²"))
		checkDecision(t, result, Matched, "75")
		checkCandidates(t, result, "75")
	})
	t.Run("compound", func(t *testing.T) {
		m := newMatcher(t, boq("PANEL", "Alüminyum panel 90 × 210 cm", "adet"),
			boq("REVERSED", "Alüminyum panel 210 x 90 cm", "adet"))
		result := m.MatchItem(quoted("Aluminyum panel 900 mm x 2.1 m.", "pcs"))
		checkDecision(t, result, Matched, "PANEL")
		checkCandidates(t, result, "PANEL")
	})
}

func TestFullConcreteGradesExactIncompleteOrConflicting(t *testing.T) {
	m := newMatcher(t, boq("C", "Temel için C30/37 hazır beton dökümü", "m³"))
	for _, test := range []struct {
		grade, code string
		status      Status
	}{
		{"C-30 / 37", "C", Matched},
		{"C30", "", Ambiguous},
		{"C30/40", "", Unmatched},
	} {
		t.Run(test.grade, func(t *testing.T) {
			result := m.MatchItem(quoted("Temel için "+test.grade+" hazır beton dökümü", "m3"))
			checkDecision(t, result, test.status, test.code)
			if test.status == Ambiguous {
				checkCandidates(t, result, "C")
				checkUnconfirmed(t, result.Candidates[0], "concrete grade")
			}
		})
	}
}

func TestBilingualDescriptionAliasesStillRequireMatchingSpecs(t *testing.T) {
	m := newMatcher(t, boq("A", "C28/35 pompa ile hazır beton dökümü", "m3"),
		boq("B", "C32/40 pompa ile hazır beton dökümü", "m3"))
	result := m.MatchItem(quoted("C28/35 ready-mixed concrete, pump placed", "cubic metres"))
	checkDecision(t, result, Matched, "A")
	checkCandidates(t, result, "A")
}

func TestMissingWidthOrApplicationKeepsPlausibleCandidates(t *testing.T) {
	t.Run("width", func(t *testing.T) {
		m := newMatcher(t, boq("A", "85 cm genişliğinde EI90 yangın kapısı", "pcs"),
			boq("B", "105 cm genişliğinde EI90 yangın kapısı", "pcs"))
		result := m.MatchItem(quoted("Fire-rated door EI90", "adet"))
		checkDecision(t, result, Ambiguous, "")
		checkCandidates(t, result, "A", "B")
		for _, candidate := range result.Candidates {
			checkUnconfirmed(t, candidate, "width")
		}
		checkDecision(t, m.MatchItem(quoted("Fire-rated door EI90 width 0.85 m", "pcs")), Matched, "A")
		checkDecision(t, m.MatchItem(quoted("Fire-rated door EI90 width 95 cm", "pcs")), Unmatched, "")
	})
	t.Run("application", func(t *testing.T) {
		m := newMatcher(t, boq("ROOF", "Çatı için mineral yün yalıtımı 60 mm", "m2"),
			boq("WALL", "Duvar için mineral yün yalıtımı 60 mm", "m2"))
		result := m.MatchItem(quoted("Mineral wool insulation 6 cm", "m²"))
		checkDecision(t, result, Ambiguous, "")
		checkCandidates(t, result, "ROOF", "WALL")
		checkDecision(t, m.MatchItem(quoted("Wall mineral wool insulation 6 cm", "m²")), Matched, "WALL")
	})
}

func TestSteelGradeAndFireRatingConflictsBlockLinks(t *testing.T) {
	for _, test := range []struct{ code, text, same, conflict string }{
		{"S", "B420C çelik donatı çubuğu", "B 420 C çelik donatı çubuğu", "B420A çelik donatı çubuğu"},
		{"F", "EI90 duman sızdırmaz kapı", "EI-90 duman sızdırmaz kapı", "REI90 duman sızdırmaz kapı"},
	} {
		t.Run(test.code, func(t *testing.T) {
			m := newMatcher(t, boq(test.code, test.text, "pcs"))
			checkDecision(t, m.MatchItem(quoted(test.same, "adet")), Matched, test.code)
			checkDecision(t, m.MatchItem(quoted(test.conflict, "adet")), Unmatched, "")
		})
	}
}

func TestAmbiguousCandidatesAreReportedWithoutALink(t *testing.T) {
	m := newMatcher(t, boq("B", "Seramik zemin kaplaması", "m2"), boq("A", "Seramik zemin kaplaması", "m²"))
	result := m.MatchItem(quoted("Seramik zemin kaplaması", "m2"))
	checkDecision(t, result, Ambiguous, "")
	checkCandidates(t, result, "A", "B")
}

func TestRowsStayInOrderAndMissingValuesDoNotLink(t *testing.T) {
	m := newMatcher(t, boq("P", "Metal levha", "pcs"))
	results := m.MatchQuotation(Quotation{ExtractedItems: []QuotationItem{
		quoted("Metal levha", "adet"), quoted("Metal levha", ""), quoted("???", "pcs"), quoted("Metal levha", "pcs"),
	}})
	if len(results) != 4 {
		t.Fatalf("got %d rows, want 4", len(results))
	}
	checkDecision(t, results[0], Matched, "P")
	checkDecision(t, results[1], Unmatched, "")
	checkDecision(t, results[2], Unmatched, "")
	checkDecision(t, results[3], Matched, "P")
}

func TestQuotationJSONDoesNotGuessMissingOrNonStringFields(t *testing.T) {
	m := newMatcher(t, boq("P", "Metal levha", "pcs"))
	for _, input := range []string{
		`{"rawDescription": "Metal levha"}`,
		`{"rawDescription": "Metal levha", "unitOfMeasure": null}`,
		`{"rawDescription": "Metal levha", "unitOfMeasure": 7}`,
		`{"rawDescription": ["Metal levha"], "unitOfMeasure": "pcs"}`,
		`{"RAWDESCRIPTION": "Metal levha", "unitOfMeasure": "pcs"}`,
	} {
		t.Run(input, func(t *testing.T) {
			var row QuotationItem
			if err := json.Unmarshal([]byte(input), &row); err != nil {
				t.Fatal(err)
			}
			result := m.MatchItem(row)
			checkDecision(t, result, Unmatched, "")
			checkCandidates(t, result)
			if result.Reason != "The quotation row needs a description and unit." {
				t.Fatalf("unexpected reason %q", result.Reason)
			}
		})
	}
}

func TestInvalidMasterRowsFailEarly(t *testing.T) {
	for _, test := range []struct {
		name string
		rows []BOQItem
	}{
		{"missing unit", []BOQItem{boq("A", "Metal levha", "")}},
		{"missing description", []BOQItem{boq("A", "", "pcs")}},
		{"duplicate code", []BOQItem{boq("A", "Metal levha", "pcs"), boq("A", "Beton levha", "pcs")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewBOQMatcher(test.rows); err == nil {
				t.Fatal("invalid rows accepted")
			}
		})
	}
}

func TestDecisionOrderAndThresholds(t *testing.T) {
	for _, test := range []struct {
		name   string
		rows   []BOQItem
		status Status
		reason string
	}{
		{"below candidate floor", []BOQItem{boq("A", "abcdewxyzq", "pcs")}, Unmatched,
			"No sufficiently similar BOQ row has compatible units and specifications."},
		{"candidate floor", []BOQItem{boq("A", "abcdefwxyz", "pcs")}, Ambiguous,
			"The best candidate falls below the automatic match threshold."},
		{"tie before automatic threshold", []BOQItem{boq("A", "abcdefwxyz", "pcs"), boq("B", "abcdefwxyz", "pcs")}, Ambiguous,
			"Multiple BOQ candidates have similar scores."},
		{"missing spec before tie", []BOQItem{boq("A", "abcdefwxyz 12 mm", "pcs"), boq("B", "abcdefwxyz 15 mm", "pcs")}, Ambiguous,
			"The best candidate needs confirmation of measurement."},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := newMatcher(t, test.rows...).MatchItem(quoted("abcdefghij", "pcs"))
			checkDecision(t, result, test.status, "")
			if result.Reason != test.reason {
				t.Fatalf("reason = %q, want %q", result.Reason, test.reason)
			}
		})
	}
}

func TestNormalizationBoundariesAndDecimalPrecision(t *testing.T) {
	// Expected text and specifications come from the Python reference.
	for _, test := range []struct {
		input, text string
		specs       map[string]specSet
	}{
		{"Boru Ø 85 mm.2", "boru mm.2", map[string]specSet{"diameter": {"85": {}}}},
		{"Boru Ø 85mm.2", "boru ø 2", map[string]specSet{"measurement": {"85": {}}}},
		{"Boru 12mm diameter.2", "boru 2", map[string]specSet{"diameter": {"12": {}}}},
		{"Boru DN ٨٠ mmextra", "boru mmextra", map[string]specSet{"nominal diameter": {"80": {}}}},
		{"Boru 0.90 × ٢١٠ cm × 2.5 cm", "boru", map[string]specSet{"compound dimensions": {"9mmx2100mmx25mm": {}}}},
		{"Boru 90 x 210 cm x 2 m", "boru", map[string]specSet{"compound dimensions": {"90x2100mmx2000mm": {}}}},
		{"C30/37 / 44 beton", "c30/37 44 beton", map[string]specSet{}},
		{"C30 / 37a beton", "c30 37a beton", map[string]specSet{}},
		{"Kapı width85mm", "kapi width85mm", map[string]specSet{}},
		{"Kapı width:85mm EI 090", "kapi", map[string]specSet{"width": {"85": {}}, "fire rating": {"ei90": {}}}},
		{"Kapı 1.85mm width", "kapi", map[string]specSet{"width": {"1.85": {}}}},
		{"S 355 J2 boru", "boru", map[string]specSet{"steel grade": {"s355j2": {}}}},
		{"Straße φύ YALITIMI", "strasse øυ yalitim", map[string]specSet{}},
		{"Boru ø12_c", "boru ø12 c", map[string]specSet{}},
		{"Boru ø12ϩ", "boru ø12ϩ", map[string]specSet{}},
		{"Boru ø12\u034f mm", "boru mm", map[string]specSet{"diameter": {"12": {}}}},
		{"a" + strings.Repeat("\u0301", 42) + "b", "ab", map[string]specSet{}},
		{"Boru 1.2345678901234567890123456785 mm", "boru", map[string]specSet{"measurement": {"1.234567890123456789012345678": {}}}},
		{"Boru 1.2345678901234567890123456795 mm", "boru", map[string]specSet{"measurement": {"1.23456789012345678901234568": {}}}},
		{"Boru 99.999999999999999999999999995 m", "boru", map[string]specSet{"measurement": {"100000": {}}}},
		{"Boru x １.٠١ cm", "boru x", map[string]specSet{"measurement": {"10.1": {}}}},
		{"A DN 𝟠𝟘", "a", map[string]specSet{"nominal diameter": {"80": {}}}},
	} {
		t.Run(test.input, func(t *testing.T) {
			result := normalizeDescription(test.input)
			if result.text != test.text || !reflect.DeepEqual(result.specs, test.specs) {
				t.Fatalf("got (%q, %v), want (%q, %v)", result.text, result.specs, test.text, test.specs)
			}
		})
	}
}
