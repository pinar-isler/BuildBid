package providers

import (
	"context"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/pinar-isler/BuildBid/validation/extraction"
)

const (
	DefaultGeminiModel = "gemini-3.5-flash-lite"
	geminiModelsURL    = "https://generativelanguage.googleapis.com/v1beta/models"
)

type GeminiProvider struct {
	adapter adapter
	to      endpoint
}

var _ extraction.Provider = (*GeminiProvider)(nil)

func NewGeminiProvider(transport http.RoundTripper, schemas fs.FS, options Options) (*GeminiProvider, error) {
	a, err := newAdapter(transport, schemas, options, DefaultGeminiModel)
	if err != nil {
		return nil, err
	}
	modelPath := strings.ReplaceAll(url.QueryEscape(a.model), "+", "%20")
	return &GeminiProvider{adapter: a, to: endpoint{
		name: "Gemini", url: geminiModelsURL + "/" + modelPath + ":generateContent",
		keyHeader: "x-goog-api-key", envNames: []string{"GOOGLE_API_KEY", "GEMINI_API_KEY"},
	}}, nil
}

func (p *GeminiProvider) Extract(ctx context.Context, document extraction.Document, kind extraction.Kind) (extraction.ProviderResponse, error) {
	schema, err := p.adapter.requestSchema(kind)
	if err != nil {
		return extraction.ProviderResponse{}, err
	}
	encoded, err := encodeMedia(document)
	if err != nil {
		return extraction.ProviderResponse{}, err
	}
	body, err := p.adapter.postJSON(ctx, p.to, map[string]any{
		"contents": []any{map[string]any{
			"role": "user",
			"parts": []any{
				map[string]any{"text": prompt(kind)},
				map[string]any{"inlineData": map[string]any{"mimeType": document.MediaType, "data": encoded}},
			},
		}},
		"generationConfig": map[string]any{"responseMimeType": "application/json", "responseJsonSchema": schema.body},
	})
	if err != nil {
		return extraction.ProviderResponse{}, err
	}
	return geminiPayload(body)
}

func geminiPayload(body map[string]any) (extraction.ProviderResponse, error) {
	if feedback, ok := body["promptFeedback"].(map[string]any); ok && truthy(feedback["blockReason"]) {
		return extraction.ProviderResponse{}, providerError("Gemini blocked the extraction request.")
	}
	candidates, err := objects(body["candidates"], "Gemini returned invalid candidates.")
	if err != nil {
		return extraction.ProviderResponse{}, err
	}
	if len(candidates) != 1 {
		return extraction.ProviderResponse{}, providerError("Gemini did not return exactly one extraction candidate.")
	}
	candidate := candidates[0]
	if candidate["finishReason"] != "STOP" {
		return extraction.ProviderResponse{}, providerError("Gemini did not complete the extraction response.")
	}
	content, ok := candidate["content"].(map[string]any)
	if !ok {
		return extraction.ProviderResponse{}, providerError("Gemini returned invalid candidate content.")
	}
	parts, err := objects(content["parts"], "Gemini returned invalid content parts.")
	if err != nil {
		return extraction.ProviderResponse{}, err
	}
	var text strings.Builder
	for _, part := range parts {
		if _, hasText := part["text"]; !hasText || truthy(part["thought"]) {
			continue
		}
		fragment, err := partText(part, "Gemini")
		if err != nil {
			return extraction.ProviderResponse{}, err
		}
		text.WriteString(fragment)
	}
	return responsePayload(text.String(), "Gemini")
}
