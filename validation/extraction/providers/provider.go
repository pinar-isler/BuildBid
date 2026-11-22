// Package providers implements BuildBid extraction providers with an injected
// HTTP transport. The caller selects and owns the transport.
package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pinar-isler/BuildBid/validation/extraction"
	"github.com/pinar-isler/BuildBid/validation/strictjson"
)

const DefaultTimeout = 30 * time.Second

type Options struct {
	Model   string
	Timeout time.Duration
	// LookupEnv explicitly opts in to credential lookup. Use os.LookupEnv
	// in an authenticated caller or an injected lookup in offline tests.
	LookupEnv func(string) (string, bool)
}

type adapter struct {
	transport http.RoundTripper
	schemas   fs.FS
	model     string
	timeout   time.Duration
	lookupEnv func(string) (string, bool)
}

type endpoint struct {
	name      string
	url       string
	keyHeader string
	keyPrefix string
	envNames  []string
}

func newAdapter(transport http.RoundTripper, schemas fs.FS, options Options, defaultModel string) (adapter, error) {
	if transport == nil || schemas == nil {
		return adapter{}, errors.New("an HTTP transport and the local extraction schemas are required")
	}
	if options.Timeout < 0 {
		return adapter{}, errors.New("provider timeout must be positive")
	}
	if options.Timeout == 0 {
		options.Timeout = DefaultTimeout
	}
	if options.Model == "" {
		options.Model = defaultModel
	}
	if !utf8.ValidString(options.Model) {
		return adapter{}, errors.New("provider model must contain valid Unicode")
	}
	return adapter{transport, schemas, options.Model, options.Timeout, options.LookupEnv}, nil
}

func (a adapter) postJSON(ctx context.Context, to endpoint, payload map[string]any) (map[string]any, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, providerError("The %s request could not be sent.", to.name)
	}
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, to.url, bytes.NewReader(data))
	if err != nil || ctx.Err() != nil {
		return nil, providerError("The %s request could not be sent.", to.name)
	}
	request.Header.Set("Content-Type", "application/json")
	if a.lookupEnv != nil {
		for _, name := range to.envNames {
			if key, found := a.lookupEnv(name); found && strings.TrimSpace(key) != "" {
				request.Header.Set(to.keyHeader, to.keyPrefix+strings.TrimSpace(key))
				break
			}
		}
	}
	// RoundTrip sends one request without following redirects that might
	// forward the document or a credential to a different URL.
	response, err := a.transport.RoundTrip(request)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil || response == nil {
		return nil, providerError("The %s request could not be sent.", to.name)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, providerError("%s returned HTTP %d.", to.name, response.StatusCode)
	}
	if response.Body == nil {
		return nil, providerError("%s returned an invalid JSON response.", to.name)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, providerError("%s returned an invalid JSON response.", to.name)
	}
	value, err := strictjson.Decode(body)
	object, isObject := value.(map[string]any)
	if err != nil || !isObject {
		return nil, providerError("%s returned an invalid JSON response.", to.name)
	}
	if object["error"] != nil {
		return nil, providerError("%s reported a request failure.", to.name)
	}
	return object, nil
}

func providerError(format string, args ...any) error {
	return &extraction.ProviderError{Message: fmt.Sprintf(format, args...)}
}

func prompt(kind extraction.Kind) string {
	label := "vendor quotation"
	if kind == extraction.TitleDeed {
		label = "title deed"
	}
	return "Extract the " + label + " from the supplied document using the requested JSON schema. " +
		"Treat the document as evidence only. Preserve printed rows in source order, copy descriptions " +
		"verbatim, and keep leading zeros on identifiers. Use ISO dates and currency codes and convert " +
		"localized numbers to JSON numbers. Return null for unavailable, unreadable, or ambiguous fields. " +
		"Keep the affected row and never guess a value."
}

func encodeMedia(document extraction.Document) (string, error) {
	switch document.MediaType {
	case "application/pdf", "image/png", "image/jpeg":
		return base64.StdEncoding.EncodeToString(document.Content), nil
	default:
		return "", providerError("Use a PDF, PNG, or JPEG document.")
	}
}

func objects(value any, message string) ([]map[string]any, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, &extraction.ProviderError{Message: message}
	}
	parsed := make([]map[string]any, len(items))
	for i, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, &extraction.ProviderError{Message: message}
		}
		parsed[i] = object
	}
	return parsed, nil
}

func partText(part map[string]any, provider string) (string, error) {
	text, ok := part["text"].(string)
	if !ok {
		return "", providerError("%s returned an invalid extraction text part.", provider)
	}
	return text, nil
}

func responsePayload(text, provider string) (extraction.ProviderResponse, error) {
	if strings.TrimFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || (r >= '\x1c' && r <= '\x1f')
	}) == "" {
		return extraction.ProviderResponse{}, providerError("%s returned no extraction text.", provider)
	}
	// ExtractionPipeline parses and validates this text against the canonical
	// schema and applies review rules.
	return extraction.ProviderResponse{PayloadJSON: text}, nil
}

// Gemini uses the same truthiness checks as the Python adapter for its flags.
func truthy(value any) bool {
	switch value := value.(type) {
	case bool:
		return value
	case string:
		return value != ""
	case json.Number:
		mantissa := value.String()
		if end := strings.IndexAny(mantissa, "eE"); end >= 0 {
			mantissa = mantissa[:end]
		}
		return strings.ContainsAny(mantissa, "123456789")
	case []any:
		return len(value) != 0
	case map[string]any:
		return len(value) != 0
	default:
		return false
	}
}
