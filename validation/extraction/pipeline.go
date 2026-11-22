// Package extraction validates offline document extraction results against
// BuildBid's canonical schemas.
package extraction

import (
	"context"
	"errors"
	"maps"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pinar-isler/BuildBid/validation/strictjson"
)

type Provider interface {
	Extract(ctx context.Context, document Document, kind Kind) (ProviderResponse, error)
}

type ProviderResponse struct {
	PayloadJSON        string
	UnavailableReasons map[string]string
}

// ProviderError explicitly marks a provider message safe to return to callers.
type ProviderError struct {
	Message string
}

func (e *ProviderError) Error() string { return e.Message }

type Status string

const (
	Complete    Status = "complete"
	NeedsReview Status = "needs_review"
	Failed      Status = "failed"
)

type UnavailableField struct {
	Pointer string `json:"pointer"`
	Reason  string `json:"reason"`
}

type ExtractionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Pointer string `json:"pointer"`
}

type Result struct {
	Status            Status
	Kind              Kind
	Payload           map[string]any
	UnavailableFields []UnavailableField
	Errors            []ExtractionError
}

func (r Result) MarshalJSON() ([]byte, error) {
	fields := make([]any, len(r.UnavailableFields))
	for i, field := range r.UnavailableFields {
		fields[i] = map[string]any{"pointer": field.Pointer, "reason": field.Reason}
	}
	issues := make([]any, len(r.Errors))
	for i, issue := range r.Errors {
		issues[i] = map[string]any{"code": issue.Code, "message": issue.Message, "pointer": issue.Pointer}
	}
	return strictjson.Encode(map[string]any{
		"status": string(r.Status), "documentKind": string(r.Kind), "payload": r.Payload,
		"unavailableFields": fields, "errors": issues,
	})
}

type Pipeline struct {
	provider   Provider
	validators *Validators
	maxBytes   int64
}

func NewPipeline(provider Provider, validators *Validators, maxBytes int64) (*Pipeline, error) {
	if provider == nil || validators == nil || len(validators.byKind) == 0 {
		return nil, errors.New("an extraction provider and validators are required")
	}
	if maxBytes < 1 {
		return nil, errors.New("max_bytes must be a positive integer")
	}
	return &Pipeline{provider, validators, maxBytes}, nil
}

func (p *Pipeline) Run(ctx context.Context, path string, kind Kind) Result {
	if !kind.Supported() {
		return unsupportedKind(kind)
	}
	document, err := LoadDocument(path, p.maxBytes)
	if err != nil {
		var input *InputError
		if errors.As(err, &input) {
			return failed(kind, input.Code, input.Message)
		}
		return failed(kind, "input_unreadable", "The input could not be read.")
	}
	response, err := callProvider(ctx, p.provider, document, kind)
	if err != nil {
		message := "The extraction provider could not finish the request."
		var safe *ProviderError
		if errors.As(err, &safe) && safe != nil && utf8.ValidString(safe.Message) {
			message = safe.Message
		}
		return failed(kind, "provider_error", message)
	}
	return p.validators.ValidateResponse(kind, response)
}

func callProvider(ctx context.Context, provider Provider, doc Document, kind Kind) (response ProviderResponse, err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("extraction provider panicked")
		}
	}()
	return provider.Extract(ctx, doc, kind)
}

// ValidateResponse checks present values on a review projection while retaining
// the original rows, omissions, nulls, and exact numeric spellings.
func (v *Validators) ValidateResponse(kind Kind, response ProviderResponse) Result {
	canonical, ok := v.byKind[kind]
	if !ok {
		return unsupportedKind(kind)
	}
	payload, err := strictjson.Decode([]byte(response.PayloadJSON))
	if err != nil {
		return failed(kind, "invalid_json", "The provider returned invalid or ambiguous JSON.")
	}

	fields := []UnavailableField{}
	available := canonical.shape.reviewPayload(payload, "", &fields)
	issues := canonical.violations(available, len(fields) > 0)
	if !applyReasons(fields, response.UnavailableReasons) {
		return failed(kind, "invalid_provider_response", "Unavailable reasons must identify missing fields and contain text.")
	}
	if len(issues) != 0 {
		return Result{Status: Failed, Kind: kind, UnavailableFields: fields, Errors: issues}
	}
	status := Complete
	if len(fields) != 0 {
		status = NeedsReview
	}
	return Result{Status: status, Kind: kind, Payload: payload.(map[string]any), UnavailableFields: fields}
}

func (n *schemaNode) reviewPayload(payload any, path string, fields *[]UnavailableField) any {
	if n.typ == "array" {
		if rows, ok := payload.([]any); ok {
			available := make([]any, len(rows))
			for i, row := range rows {
				available[i] = n.items.reviewPayload(row, joinPointer(path, strconv.Itoa(i)), fields)
			}
			return available
		}
	}
	object, ok := payload.(map[string]any)
	if n.typ != "object" || !ok {
		return payload
	}
	available := maps.Clone(object)
	for _, field := range n.fields {
		pointer := joinPointer(path, field.name)
		value, exists := object[field.name]
		if field.required {
			if reason := unavailableReason(field.node.typ, value); reason != "" {
				*fields = append(*fields, UnavailableField{Pointer: pointer, Reason: reason})
				delete(available, field.name)
				if _, blank := value.(string); blank {
					object[field.name] = nil
				}
				continue
			}
		}
		if exists {
			available[field.name] = field.node.reviewPayload(value, pointer, fields)
		}
	}
	return available
}

func unavailableReason(typ string, value any) string {
	switch value := value.(type) {
	case nil:
		return "The provider did not supply a value."
	case string:
		if typ == "string" && blank(value) {
			return "The provider supplied a blank value."
		}
	case []any:
		if typ == "array" && len(value) == 0 {
			return "The provider supplied no rows."
		}
	}
	return ""
}

func blank(value string) bool {
	return strings.TrimFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || (r >= '\x1c' && r <= '\x1f')
	}) == ""
}

func applyReasons(fields []UnavailableField, reasons map[string]string) bool {
	applied := 0
	for i, field := range fields {
		if reason, exists := reasons[field.Pointer]; exists {
			if blank(reason) || !utf8.ValidString(reason) {
				return false
			}
			fields[i].Reason = reason
			applied++
		}
	}
	return applied == len(reasons)
}

var pointerEscapes = strings.NewReplacer("~", "~0", "/", "~1")

func joinPointer(path, part string) string {
	return path + "/" + pointerEscapes.Replace(part)
}

func failed(kind Kind, code, message string) Result {
	return Result{Status: Failed, Kind: kind, Errors: []ExtractionError{{Code: code, Message: message}}}
}

func unsupportedKind(kind Kind) Result {
	return failed(kind, "unsupported_kind", "Use quotation or title_deed as the document kind.")
}
