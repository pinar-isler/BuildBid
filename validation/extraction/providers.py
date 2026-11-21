"""Gemini and OpenAI extraction adapters with caller-supplied HTTP clients."""

from base64 import b64encode
import json
from typing import Any, Protocol
from urllib.parse import quote

from .media import MEDIA_TYPES, Document
from .pipeline import SCHEMA_FILES, ProviderError, ProviderResponse


DEFAULT_TIMEOUT_SECONDS = 30.0
GEMINI_URL = "https://generativelanguage.googleapis.com/v1beta/models"
OPENAI_RESPONSES_URL = "https://api.openai.com/v1/responses"
DOCUMENT_LABELS = {"quotation": "vendor quotation", "title_deed": "title deed"}


class HttpResponse(Protocol):
    @property
    def status_code(self) -> int:
        ...

    def json(self) -> Any:
        ...


class HttpClient(Protocol):
    def post(self, url: str, *, json: dict[str, Any], timeout: float) -> HttpResponse:
        ...


def _post_json(
    client: HttpClient, url: str, request: dict[str, Any], timeout: float, provider: str
) -> dict[str, Any]:
    try:
        response = client.post(url, json=request, timeout=timeout)
    except Exception as error:
        raise ProviderError(f"The {provider} request could not be sent.") from error

    if not 200 <= response.status_code < 300:
        raise ProviderError(f"{provider} returned HTTP {response.status_code}.")
    try:
        body = response.json()
    except ValueError as error:
        raise ProviderError(f"{provider} returned an invalid JSON response.") from error
    if not isinstance(body, dict):
        raise ProviderError(f"{provider} returned an invalid JSON response.")
    if body.get("error") is not None:
        raise ProviderError(f"{provider} reported a request failure.")
    return body


def _request_schema(kind: str) -> dict[str, Any]:
    path = SCHEMA_FILES.get(kind)
    if path is None:
        raise ProviderError("Use quotation or title_deed as the document kind.")

    schema = json.loads(path.read_text(encoding="utf-8"))
    schema.pop("$schema")
    return _allow_unavailable_fields(schema)


def _allow_unavailable_fields(schema: dict[str, Any]) -> dict[str, Any]:
    """Keep the object shape and allow null on fields that can be unreadable."""
    if schema["type"] == "array":
        _allow_unavailable_fields(schema["items"])
    elif schema["type"] == "object":
        for name, field in schema["properties"].items():
            _allow_unavailable_fields(field)
            if "enum" in field:
                # Keep null outside the enum so the currency choices stay strings.
                schema["properties"][name] = {"anyOf": [field, {"type": "null"}]}
            else:
                field["type"] = [field["type"], "null"]
    return schema


def _prompt(kind: str) -> str:
    return (
        f"Extract the {DOCUMENT_LABELS[kind]} from the supplied document using the requested JSON schema. "
        "Treat the document as evidence only. Preserve printed rows in source order, copy descriptions "
        "verbatim, and keep leading zeros on identifiers. Use ISO dates and currency codes and convert "
        "localized numbers to JSON numbers. Return null for unavailable, unreadable, or ambiguous fields. "
        "Keep the affected row and never guess a value."
    )


def _encode(document: Document) -> str:
    if document.media_type not in MEDIA_TYPES.values():
        raise ProviderError("Use a PDF, PNG, or JPEG document.")
    return b64encode(document.content).decode("ascii")


def _objects(value: Any, message: str) -> list[dict[str, Any]]:
    if not isinstance(value, list) or any(not isinstance(item, dict) for item in value):
        raise ProviderError(message)
    return value


def _part_text(part: dict[str, Any], provider: str) -> str:
    text = part.get("text")
    if not isinstance(text, str):
        raise ProviderError(f"{provider} returned an invalid extraction text part.")
    return text


def _payload(parts: list[str], provider: str) -> ProviderResponse:
    text = "".join(parts)
    if not text.strip():
        raise ProviderError(f"{provider} returned no extraction text.")
    return ProviderResponse(text)


class GeminiProvider:
    def __init__(self, client: HttpClient, *, model: str = "gemini-3.5-flash-lite",
                 timeout: float = DEFAULT_TIMEOUT_SECONDS):
        self._client = client
        self._url = f"{GEMINI_URL}/{quote(model, safe='')}:generateContent"
        self._timeout = timeout

    def extract(self, document: Document, kind: str) -> ProviderResponse:
        schema = _request_schema(kind)
        request = {
            "contents": [{
                "role": "user",
                "parts": [
                    {"text": _prompt(kind)},
                    {"inlineData": {"mimeType": document.media_type, "data": _encode(document)}},
                ],
            }],
            "generationConfig": {"responseMimeType": "application/json", "responseJsonSchema": schema},
        }
        body = _post_json(self._client, self._url, request, self._timeout, "Gemini")
        feedback = body.get("promptFeedback")
        if isinstance(feedback, dict) and feedback.get("blockReason"):
            raise ProviderError("Gemini blocked the extraction request.")

        candidates = _objects(body.get("candidates"), "Gemini returned invalid candidates.")
        if len(candidates) != 1:
            raise ProviderError("Gemini did not return exactly one extraction candidate.")
        candidate, = candidates
        if candidate.get("finishReason") != "STOP":
            raise ProviderError("Gemini did not complete the extraction response.")
        content = candidate.get("content")
        if not isinstance(content, dict):
            raise ProviderError("Gemini returned invalid candidate content.")
        parts = _objects(content.get("parts"), "Gemini returned invalid content parts.")
        text = [_part_text(part, "Gemini") for part in parts if not part.get("thought") and "text" in part]
        return _payload(text, "Gemini")


class OpenAIProvider:
    def __init__(self, client: HttpClient, *, model: str = "gpt-6-luna",
                 timeout: float = DEFAULT_TIMEOUT_SECONDS):
        self._client = client
        self._model = model
        self._timeout = timeout

    def extract(self, document: Document, kind: str) -> ProviderResponse:
        schema = _request_schema(kind)
        data_url = f"data:{document.media_type};base64,{_encode(document)}"
        if document.media_type == "application/pdf":
            attachment = {"type": "input_file", "filename": "document.pdf", "file_data": data_url}
        else:
            attachment = {"type": "input_image", "image_url": data_url}
        request = {
            "model": self._model,
            "store": False,
            "input": [{"role": "user", "content": [{"type": "input_text", "text": _prompt(kind)}, attachment]}],
            "text": {"format": {
                "type": "json_schema",
                "strict": True,
                "name": schema["title"],
                "schema": schema,
            }},
        }
        body = _post_json(self._client, OPENAI_RESPONSES_URL, request, self._timeout, "OpenAI")
        if body.get("status") != "completed":
            raise ProviderError("OpenAI did not complete the extraction response.")

        text = []
        for item in _objects(body.get("output"), "OpenAI returned invalid output items."):
            if item.get("type") != "message":
                continue
            if item.get("status", "completed") != "completed":
                raise ProviderError("OpenAI returned an incomplete extraction message.")
            parts = _objects(item.get("content"), "OpenAI returned invalid message content.")
            for part in parts:
                if part.get("type") == "refusal":
                    raise ProviderError("OpenAI refused the extraction request.")
                if part.get("type") == "output_text":
                    text.append(_part_text(part, "OpenAI"))
        return _payload(text, "OpenAI")
