package providers

import (
	"context"
	"io/fs"
	"net/http"
	"strings"

	"github.com/pinar-isler/BuildBid/validation/extraction"
)

const (
	DefaultOpenAIModel = "gpt-6-luna"
	openAIResponsesURL = "https://api.openai.com/v1/responses"
)

type OpenAIProvider struct {
	adapter adapter
}

var _ extraction.Provider = (*OpenAIProvider)(nil)

func NewOpenAIProvider(transport http.RoundTripper, schemas fs.FS, options Options) (*OpenAIProvider, error) {
	a, err := newAdapter(transport, schemas, options, DefaultOpenAIModel)
	if err != nil {
		return nil, err
	}
	return &OpenAIProvider{adapter: a}, nil
}

func (p *OpenAIProvider) Extract(ctx context.Context, document extraction.Document, kind extraction.Kind) (extraction.ProviderResponse, error) {
	schema, err := p.adapter.requestSchema(kind)
	if err != nil {
		return extraction.ProviderResponse{}, err
	}
	encoded, err := encodeMedia(document)
	if err != nil {
		return extraction.ProviderResponse{}, err
	}
	dataURL := "data:" + document.MediaType + ";base64," + encoded
	attachment := map[string]any{"type": "input_image", "image_url": dataURL}
	if document.MediaType == "application/pdf" {
		attachment = map[string]any{"type": "input_file", "filename": "document.pdf", "file_data": dataURL}
	}
	request := map[string]any{
		"model": p.adapter.model,
		"store": false,
		"input": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": prompt(kind)},
				attachment,
			},
		}},
		"text": map[string]any{"format": map[string]any{
			"type": "json_schema", "strict": true, "name": schema.title, "schema": schema.body,
		}},
	}
	body, err := p.adapter.postJSON(ctx, endpoint{
		name: "OpenAI", url: openAIResponsesURL, keyHeader: "Authorization",
		keyPrefix: "Bearer ", envNames: []string{"OPENAI_API_KEY"},
	}, request)
	if err != nil {
		return extraction.ProviderResponse{}, err
	}
	return openAIPayload(body)
}

func openAIPayload(body map[string]any) (extraction.ProviderResponse, error) {
	if body["status"] != "completed" {
		return extraction.ProviderResponse{}, providerError("OpenAI did not complete the extraction response.")
	}
	items, err := objects(body["output"], "OpenAI returned invalid output items.")
	if err != nil {
		return extraction.ProviderResponse{}, err
	}
	var text strings.Builder
	for _, item := range items {
		if item["type"] != "message" {
			continue
		}
		if status, exists := item["status"]; exists && status != "completed" {
			return extraction.ProviderResponse{}, providerError("OpenAI returned an incomplete extraction message.")
		}
		parts, err := objects(item["content"], "OpenAI returned invalid message content.")
		if err != nil {
			return extraction.ProviderResponse{}, err
		}
		for _, part := range parts {
			switch part["type"] {
			case "refusal":
				return extraction.ProviderResponse{}, providerError("OpenAI refused the extraction request.")
			case "output_text":
				fragment, err := partText(part, "OpenAI")
				if err != nil {
					return extraction.ProviderResponse{}, err
				}
				text.WriteString(fragment)
			}
		}
	}
	return responsePayload(text.String(), "OpenAI")
}
