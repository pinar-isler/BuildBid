package extraction

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func loadManifest(t *testing.T) manifest {
	t.Helper()
	data := readFile(t, filepath.Join(corpusDir, "manifest.json"))
	_ = objectJSON(t, string(data))
	var result manifest
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func reviewedHashes(t *testing.T) map[string]string {
	t.Helper()
	data := readFile(t, filepath.Join(corpusDir, "SHA256SUMS"))
	hashes := map[string]string{}
	checksum := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		digest, name, ok := strings.Cut(line, "  ")
		if !ok || !checksum.MatchString(digest) || name == "" || hashes[name] != "" {
			t.Fatalf("invalid checksum entry %q", line)
		}
		hashes[name] = digest
	}
	return hashes
}

func verifySHA256(data []byte, want string) error {
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != want {
		return fmt.Errorf("SHA-256 mismatch: got %s, want %s", got, want)
	}
	return nil
}

func folderEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func checkInventory(t *testing.T, got, want []string) {
	t.Helper()
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("inventory = %v, want %v", got, want)
	}
}

func TestOriginalCanonicalSchemaBytes(t *testing.T) {
	for name, digest := range map[string]string{
		"Vendor_Quotation_Schema": "056f0545f3c52ec130295b4c9a3bcf5a985ca73deaaf6c949d2e3af99bc554c9",
		"Title_deed_Schema":       "74bc43a743da44961ef48e8e4341799599cadc1e18c49c69738f8b980cb44345",
	} {
		t.Run(name, func(t *testing.T) {
			if err := verifySHA256(readFile(t, filepath.Join(schemaDir, name)), digest); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReviewedCorpusArtifactIntegrity(t *testing.T) {
	hashes := reviewedHashes(t)
	want := []string{"manifest.json", "master-boq.json"}
	for split := range loadManifest(t).Splits {
		for _, folder := range []string{"documents", "ground-truth"} {
			for _, name := range folderEntries(t, filepath.Join(corpusDir, split, folder)) {
				want = append(want, path.Join(split, folder, name))
			}
		}
	}
	checkInventory(t, slices.Collect(maps.Keys(hashes)), want)
	if len(hashes) != 22 {
		t.Fatalf("verified artifact inventory has %d entries, want 22", len(hashes))
	}
	for _, name := range want {
		t.Run(name, func(t *testing.T) {
			if err := verifySHA256(readFile(t, filepath.Join(corpusDir, name)), hashes[name]); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCorpusInventoryAndMediaIntegrity(t *testing.T) {
	corpus := loadManifest(t)
	if corpus.Version != 1 {
		t.Fatalf("corpus version = %d, want 1", corpus.Version)
	}
	checkInventory(t, slices.Collect(maps.Keys(corpus.Splits)), []string{"development", "held-out"})
	seenIDs, seenHashes := map[string]bool{}, map[string]bool{}
	for split, group := range corpus.Splits {
		names := []string{}
		formats := map[string]bool{}
		dir := filepath.Join(corpusDir, split, "documents")
		for _, entry := range group.Documents {
			names = append(names, entry.File)
			t.Run(entry.ID, func(t *testing.T) {
				ext := filepath.Ext(entry.File)
				if seenIDs[entry.ID] || seenHashes[entry.SHA256] || !entry.Kind.Supported() ||
					(entry.Outcome != "complete" && entry.Outcome != "incomplete") ||
					entry.ID+ext != entry.File {
					t.Fatalf("invalid fixture metadata: %+v", entry)
				}
				document, err := LoadDocument(filepath.Join(dir, entry.File), DefaultMaxBytes)
				if err != nil || document.SHA256 != entry.SHA256 {
					t.Fatalf("invalid fixture media or hash: %v", err)
				}
				switch ext {
				case ".pdf":
					if !bytes.HasSuffix(bytes.TrimSpace(document.Content), []byte("%%EOF")) {
						t.Fatal("PDF has no EOF trailer")
					}
				case ".png":
					if binary.BigEndian.Uint32(document.Content[16:20]) < 1000 ||
						binary.BigEndian.Uint32(document.Content[20:24]) < 1000 {
						t.Fatal("fixture PNG is smaller than the reviewed dimensions")
					}
				default:
					t.Fatalf("unexpected corpus extension %q", ext)
				}
				seenIDs[entry.ID], seenHashes[entry.SHA256] = true, true
				if entry.Outcome == "complete" {
					formats[string(entry.Kind)+ext] = true
				}
			})
		}
		checkInventory(t, folderEntries(t, dir), names)
		checkInventory(t, slices.Collect(maps.Keys(formats)), []string{
			"quotation.pdf", "quotation.png", "title_deed.pdf", "title_deed.png",
		})
	}
}

func requiredScalarPointer(shape *schemaNode, pointer string, rowCount int) bool {
	tokens, ok := strings.CutPrefix(pointer, "/")
	if !ok {
		return false
	}
	for _, part := range strings.Split(tokens, "/") {
		if shape.typ == "array" {
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= rowCount || strconv.Itoa(index) != part {
				return false
			}
			shape = shape.items
			continue
		}
		index := slices.IndexFunc(shape.fields, func(field schemaField) bool {
			return field.name == part && field.required
		})
		if index < 0 {
			return false
		}
		shape = shape.fields[index].node
	}
	return shape.typ == "string" || shape.typ == "number" || shape.typ == "boolean"
}

func checkIncompleteAnnotation(shape *schemaNode, entry map[string]any) error {
	count, ok := entry["sourceRowCount"].(json.Number)
	rowCount, err := count.Int64()
	if !ok || err != nil || rowCount < 1 || int64(int(rowCount)) != rowCount {
		return errors.New("sourceRowCount must be a positive integer")
	}
	fields, ok := entry["unavailableFields"].([]any)
	if !ok || len(fields) == 0 {
		return errors.New("unavailableFields must be a non-empty list")
	}
	seen := map[string]bool{}
	for _, value := range fields {
		field, ok := value.(map[string]any)
		pointer, isPointer := field["pointer"].(string)
		reason, isReason := field["reason"].(string)
		if !ok || len(field) != 2 || !isPointer || !isReason {
			return errors.New("each unavailable field must have a pointer and a reason")
		}
		if !requiredScalarPointer(shape, pointer, int(rowCount)) {
			return errors.New("pointer must identify a required scalar within the source")
		}
		if seen[pointer] {
			return errors.New("duplicate unavailable pointer")
		}
		if blank(reason) {
			return errors.New("missing reason for unavailable pointer")
		}
		seen[pointer] = true
	}
	return nil
}

func TestCorpusGroundTruthAndIncompleteExpectations(t *testing.T) {
	validators := testValidators(t)
	patternDigits := regexp.MustCompile(`^[0-9]+$`)
	patternID := regexp.MustCompile(`^SYN-(DEV|HOLD)-[0-9]{3}$`)
	patternShare := regexp.MustCompile(`^[1-9][0-9]*/[1-9][0-9]*$`)
	shareholderIDs, incompleteKinds := map[string]bool{}, map[Kind]bool{}
	rawGroups := objectJSON(t, string(readFile(t, filepath.Join(corpusDir, "manifest.json"))))["splits"].(map[string]any)
	for split, group := range loadManifest(t).Splits {
		dir := filepath.Join(corpusDir, split, "ground-truth")
		expectedFiles := []string{"reconciliation.json"}
		rawDocuments := rawGroups[split].(map[string]any)["documents"].([]any)
		for i, entry := range group.Documents {
			t.Run(entry.ID, func(t *testing.T) {
				raw := rawDocuments[i].(map[string]any)
				if entry.Outcome == "incomplete" {
					incompleteKinds[entry.Kind] = true
					if err := checkIncompleteAnnotation(validators.byKind[entry.Kind].shape, raw); err != nil {
						t.Fatal(err)
					}
					return
				}
				for _, key := range []string{"unavailableFields", "sourceRowCount"} {
					if _, exists := raw[key]; exists {
						t.Fatalf("complete entry has %q", key)
					}
				}
				name := entry.ID + ".json"
				expectedFiles = append(expectedFiles, name)
				gold := objectJSON(t, string(readFile(t, filepath.Join(dir, name))))
				issues, err := validators.ValidateCanonical(entry.Kind, gold)
				if err != nil || len(issues) != 0 {
					t.Fatalf("gold violates its canonical schema: %v %v", issues, err)
				}
				if entry.Kind == Quotation {
					rows := gold["extractedItems"].([]any)
					if blank(gold["vendorName"].(string)) || len(rows) == 0 {
						t.Fatal("quotation is missing vendor or row evidence")
					}
					for _, row := range rows {
						item := row.(map[string]any)
						if blank(item["rawDescription"].(string)) || blank(item["unitOfMeasure"].(string)) {
							t.Fatal("quotation item is missing textual evidence")
						}
					}
					return
				}
				if numberSign(gold["totalAreaM2"].(json.Number)) != 1 ||
					!patternDigits.MatchString(gold["blockAda"].(string)) ||
					!patternDigits.MatchString(gold["parcelParsel"].(string)) {
					t.Fatal("invalid cadastral identifiers or area")
				}
				sum := new(big.Rat)
				for _, value := range gold["shareholders"].([]any) {
					row := value.(map[string]any)
					id, share := row["nationalId"].(string), row["shareFraction"].(string)
					if !patternID.MatchString(id) || shareholderIDs[id] ||
						blank(row["fullName"].(string)) || !patternShare.MatchString(share) {
						t.Fatalf("invalid synthetic shareholder evidence: %v", row)
					}
					shareholderIDs[id] = true
					fraction, _ := new(big.Rat).SetString(share)
					sum.Add(sum, fraction)
				}
				if sum.Cmp(big.NewRat(1, 1)) != 0 {
					t.Fatalf("deed fractions total %s", sum)
				}
			})
		}
		checkInventory(t, folderEntries(t, dir), expectedFiles)
	}
	if len(incompleteKinds) != 2 || !incompleteKinds[Quotation] || !incompleteKinds[TitleDeed] {
		t.Fatalf("unexpected kinds of incomplete fixture: %v", incompleteKinds)
	}
}

func TestCorpusBOQAndReconciliation(t *testing.T) {
	boq := objectJSON(t, string(readFile(t, filepath.Join(corpusDir, "master-boq.json"))))
	projects := []string{}
	allCodes := map[string]bool{}
	for split, group := range loadManifest(t).Splits {
		projects = append(projects, group.BOQProjectID)
		units := map[string]string{}
		for _, value := range boq[group.BOQProjectID].([]any) {
			item := value.(map[string]any)
			code, unit := item["itemCode"].(string), item["unitOfMeasure"].(string)
			if allCodes[code] || blank(code) || blank(unit) || blank(item["description"].(string)) {
				t.Fatalf("invalid BOQ item %v", item)
			}
			allCodes[code], units[code] = true, unit
			for _, key := range []string{"estimatedQuantity", "estimatedUnitPrice"} {
				number, ok := item[key].(json.Number)
				if !ok || numberSign(number) != 1 {
					t.Fatalf("BOQ item %s has invalid %s", code, key)
				}
			}
		}
		gold := filepath.Join(corpusDir, split, "ground-truth")
		labels := objectJSON(t, string(readFile(t, filepath.Join(gold, "reconciliation.json"))))
		fixtures := []string{}
		kinds := map[string]bool{}
		for _, entry := range group.Documents {
			if entry.Kind != Quotation || entry.Outcome != "complete" {
				continue
			}
			fixtures = append(fixtures, entry.ID)
			t.Run(entry.ID, func(t *testing.T) {
				rows := objectJSON(t, string(readFile(t, filepath.Join(gold, entry.ID+".json"))))["extractedItems"].([]any)
				review := labels[entry.ID].([]any)
				if len(review) != len(rows) {
					t.Fatal("not all rows have labels")
				}
				seen := map[int]bool{}
				for _, value := range review {
					label := value.(map[string]any)
					index, err := strconv.Atoi(label["itemIndex"].(json.Number).String())
					if err != nil || index < 0 || index >= len(rows) || seen[index] {
						t.Fatalf("invalid item index %v", label)
					}
					seen[index] = true
					candidates := label["candidateItemCodes"].([]any)
					kind := label["label"].(string)
					validCount := kind == "correct" && len(candidates) == 1 ||
						kind == "unmatched" && len(candidates) == 0 ||
						kind == "ambiguous" && len(candidates) >= 2
					if blank(label["reason"].(string)) || !validCount {
						t.Fatalf("invalid label: %v", label)
					}
					kinds[kind] = true
					expectedUnit := rows[index].(map[string]any)["unitOfMeasure"].(string)
					codes := map[string]bool{}
					for _, candidate := range candidates {
						code := candidate.(string)
						unit, exists := units[code]
						if codes[code] || !exists || unit != expectedUnit {
							t.Fatalf("invalid candidate or unit: %q %q", code, unit)
						}
						codes[code] = true
					}
				}
			})
		}
		checkInventory(t, slices.Collect(maps.Keys(labels)), fixtures)
		checkInventory(t, slices.Collect(maps.Keys(kinds)), []string{"correct", "ambiguous", "unmatched"})
	}
	checkInventory(t, slices.Collect(maps.Keys(boq)), projects)
}

func TestCanonicalSchemasRejectInvalidFixtureValues(t *testing.T) {
	validators := testValidators(t)
	gold := filepath.Join(corpusDir, "development", "ground-truth")
	for _, test := range []struct {
		kind             Kind
		array, key       string
		value            any
		keyword, pointer string
	}{
		{Quotation, "", "quotationDate", "2026-02-30", "format", "/quotationDate"},
		{Quotation, "", "quotationDate", "20260312", "format", "/quotationDate"},
		{Quotation, "", "currency", "GBP", "enum", "/currency"},
		{Quotation, "", "extra", true, "additionalProperties", ""},
		{Quotation, "extractedItems", "quantity", true, "type", "/extractedItems/0/quantity"},
		{Quotation, "extractedItems", "quotedUnitPrice", json.Number("-1"), "minimum", "/extractedItems/0/quotedUnitPrice"},
		{Quotation, "extractedItems", "isTaxIncluded", "false", "type", "/extractedItems/0/isTaxIncluded"},
		{Quotation, "extractedItems", "extra", json.Number("0"), "additionalProperties", "/extractedItems/0"},
		{TitleDeed, "", "blockAda", json.Number("124"), "type", "/blockAda"},
		{TitleDeed, "", "parcelParsel", json.Number("7"), "type", "/parcelParsel"},
		{TitleDeed, "", "totalAreaM2", json.Number("-1"), "minimum", "/totalAreaM2"},
		{TitleDeed, "", "shareholders", map[string]any{}, "type", "/shareholders"},
		{Quotation, "extractedItems", "quotedUnitPrice", nil, "required", "/extractedItems/0"},
		{TitleDeed, "shareholders", "nationalId", nil, "required", "/shareholders/0"},
	} {
		t.Run(string(test.kind)+"/"+test.key, func(t *testing.T) {
			name := "Q-DEV-01.json"
			if test.kind == TitleDeed {
				name = "D-DEV-01.json"
			}
			payload := objectJSON(t, string(readFile(t, filepath.Join(gold, name))))
			target := payload
			if test.array != "" {
				target = payload[test.array].([]any)[0].(map[string]any)
			}
			if test.keyword == "required" {
				delete(target, test.key)
			} else {
				target[test.key] = test.value
			}
			issues, err := validators.ValidateCanonical(test.kind, payload)
			if err != nil || !slices.Equal(issues, []ExtractionError{{
				Code: "schema_violation", Message: "Canonical schema rule '" + test.keyword + "' failed.", Pointer: test.pointer,
			}}) {
				t.Fatalf("raw schema diagnostics differ: %v %v", issues, err)
			}
		})
	}
}

func TestIncompleteAnnotationsRejectInvalidPointersReasonsAndCounts(t *testing.T) {
	validators := testValidators(t)
	for _, test := range []struct {
		kind       Kind
		array, key string
	}{{Quotation, "extractedItems", "quotedUnitPrice"}, {TitleDeed, "shareholders", "nationalId"}} {
		for _, variant := range []struct {
			suffix string
			reason any
		}{
			{"999/" + test.key, "missing row"}, {"01/" + test.key, "leading zero"},
			{"0/unknown", "unknown field"}, {"0", "not scalar"}, {"0/" + test.key, " "}, {"0/" + test.key, nil},
		} {
			entry := map[string]any{"sourceRowCount": json.Number("1"), "unavailableFields": []any{
				map[string]any{"pointer": "/" + test.array + "/" + variant.suffix, "reason": variant.reason},
			}}
			if err := checkIncompleteAnnotation(validators.byKind[test.kind].shape, entry); err == nil {
				t.Fatalf("accepted invalid annotation %v", entry)
			}
		}
	}
	good := objectJSON(t, `{"sourceRowCount":1,"unavailableFields":[{"pointer":"/extractedItems/0/quotedUnitPrice","reason":"Unavailable."}]}`)
	for _, test := range []struct {
		key, want string
		value     any
	}{
		{"sourceRowCount", "positive integer", true},
		{"sourceRowCount", "positive integer", json.Number("0")},
		{"unavailableFields", "non-empty list", []any{}},
		{"unavailableFields", "pointer and a reason", []any{map[string]any{"pointer": "/extractedItems/0/quotedUnitPrice"}}},
		{"unavailableFields", "duplicate unavailable pointer", []any{good["unavailableFields"].([]any)[0], good["unavailableFields"].([]any)[0]}},
	} {
		entry := maps.Clone(good)
		entry[test.key] = test.value
		if err := checkIncompleteAnnotation(validators.byKind[Quotation].shape, entry); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("invalid annotation %v should return %q, got %v", entry, test.want, err)
		}
	}
}

func TestIntegrityRejectsModifiedReviewedArtifacts(t *testing.T) {
	hashes := reviewedHashes(t)
	for _, test := range []struct{ filename, original, replacement string }{
		{"development/documents/Q-DEV-01.pdf", "", "\n"},
		{"development/ground-truth/Q-DEV-01.json", "2450.75", "24500.75"},
		{"development/ground-truth/reconciliation.json", "DEV-REBAR-12", "DEV-REBAR-16"},
		{"manifest.json", "/extractedItems/0/quotedUnitPrice", "/extractedItems/999/quotedUnitPrice"},
	} {
		t.Run(test.filename, func(t *testing.T) {
			data := readFile(t, filepath.Join(corpusDir, test.filename))
			var changed []byte
			if test.original == "" {
				changed = append(slices.Clone(data), test.replacement...)
			} else {
				changed = bytes.Replace(data, []byte(test.original), []byte(test.replacement), 1)
			}
			if bytes.Equal(data, changed) || verifySHA256(changed, hashes[test.filename]) == nil {
				t.Fatal("mutation did not fail the reviewed checksum")
			}
		})
	}
}
