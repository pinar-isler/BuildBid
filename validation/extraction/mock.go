package extraction

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/pinar-isler/BuildBid/validation/strictjson"
)

type manifest struct {
	Version int              `json:"version"`
	Splits  map[string]split `json:"splits"`
}

type split struct {
	BOQProjectID string          `json:"boqProjectId"`
	Documents    []manifestEntry `json:"documents"`
}

type manifestEntry struct {
	ID                string             `json:"id"`
	Kind              Kind               `json:"kind"`
	File              string             `json:"file"`
	Outcome           string             `json:"outcome"`
	SHA256            string             `json:"sha256"`
	SourceRowCount    int                `json:"sourceRowCount"`
	UnavailableFields []UnavailableField `json:"unavailableFields"`
}

type fixtureKey struct {
	kind   Kind
	sha256 string
}

type fixture struct {
	split string
	entry manifestEntry
}

type MockProvider struct {
	corpus   fs.FS
	fixtures map[fixtureKey]fixture
}

// NewMockProvider indexes corpus v1 by document kind and content hash.
func NewMockProvider(corpus fs.FS) (*MockProvider, error) {
	data, err := fs.ReadFile(corpus, "manifest.json")
	if err != nil {
		return nil, err
	}
	if _, err := strictjson.Decode(data); err != nil {
		return nil, err
	}
	var entries manifest
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	if entries.Version != 1 || len(entries.Splits) == 0 {
		return nil, errors.New("expected a corpus v1 manifest")
	}
	provider := &MockProvider{corpus: corpus, fixtures: make(map[fixtureKey]fixture)}
	for name, group := range entries.Splits {
		for _, entry := range group.Documents {
			key := fixtureKey{entry.Kind, entry.SHA256}
			if _, exists := provider.fixtures[key]; exists {
				return nil, errors.New("duplicate mock fixture kind and hash")
			}
			if !entry.Kind.Supported() || !pathComponent(name) || !pathComponent(entry.ID) ||
				(entry.Outcome != "complete" && entry.Outcome != "incomplete") {
				return nil, errors.New("invalid mock fixture metadata")
			}
			provider.fixtures[key] = fixture{name, entry}
		}
	}
	return provider, nil
}

func pathComponent(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\")
}

func (p *MockProvider) Extract(_ context.Context, document Document, kind Kind) (ProviderResponse, error) {
	match, ok := p.fixtures[fixtureKey{kind, documentHash(document.Content)}]
	if !ok {
		return ProviderResponse{}, &ProviderError{Message: "The mock has no fixture for this document and kind."}
	}
	if match.entry.Outcome == "incomplete" {
		payload, ok := partialPayloads[match.entry.ID]
		if !ok {
			return ProviderResponse{}, errors.New("missing incomplete replay data")
		}
		reasons := make(map[string]string, len(match.entry.UnavailableFields))
		for _, field := range match.entry.UnavailableFields {
			reasons[field.Pointer] = field.Reason
		}
		return ProviderResponse{PayloadJSON: payload, UnavailableReasons: reasons}, nil
	}
	payload, err := fs.ReadFile(p.corpus, path.Join(match.split, "ground-truth", match.entry.ID+".json"))
	if err != nil {
		return ProviderResponse{}, err
	}
	if !utf8.Valid(payload) {
		return ProviderResponse{}, errors.New("fixture text is invalid UTF-8")
	}
	return ProviderResponse{PayloadJSON: string(payload)}, nil
}

// These values transcribe the visible fields from the two incomplete fixtures.
// Missing values and their reviewed reasons are left separate.
var partialPayloads = map[string]string{
	"Q-DEV-03": `{
		"vendorName":"Deneme Temel İşleri",
		"quotationDate":"2026-05-19",
		"currency":"TRY",
		"extractedItems":[{
			"rawDescription":"Temel kazısı ve nakliye",
			"quantity":31.25,
			"unitOfMeasure":"m3",
			"quotedUnitPrice":null,
			"isTaxIncluded":false
		}]
	}`,
	"D-HOLD-03": `{
		"city":"Bursa",
		"district":"Nilüfer",
		"blockAda":"0084",
		"parcelParsel":"0030",
		"totalAreaM2":2640.75,
		"shareholders":[{
			"nationalId":null,
			"fullName":"Kurgu Paydaş K",
			"shareFraction":"1/1"
		}]
	}`,
}

type MockResponseProvider struct {
	Path string
}

func (p MockResponseProvider) Extract(_ context.Context, _ Document, _ Kind) (ProviderResponse, error) {
	const message = "The mock response must be a readable UTF-8 file."
	info, err := os.Stat(p.Path)
	if err != nil || !info.Mode().IsRegular() {
		return ProviderResponse{}, &ProviderError{Message: message}
	}
	data, err := os.ReadFile(p.Path)
	if err != nil || !utf8.Valid(data) {
		return ProviderResponse{}, &ProviderError{Message: message}
	}
	return ProviderResponse{PayloadJSON: string(data)}, nil
}
