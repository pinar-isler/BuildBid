"""Provider-neutral extraction with local canonical schema validation."""

from collections.abc import Iterable, Mapping
from dataclasses import dataclass, field
from decimal import Decimal, DecimalException
import json
from pathlib import Path
from typing import Any, Literal, Optional, Protocol, Union

from jsonschema import Draft202012Validator

from .media import DEFAULT_MAX_BYTES, Document, InputError, load_document


ROOT = Path(__file__).resolve().parents[2]
SCHEMA_FILES = {
    "quotation": ROOT / "Schemas" / "Vendor_Quotation_Schema",
    "title_deed": ROOT / "Schemas" / "Title_deed_Schema",
}


class ProviderError(Exception):
    """An extraction failure with a message suitable for the caller."""


@dataclass(frozen=True)
class ProviderResponse:
    payload_json: str
    unavailable_reasons: Mapping[str, str] = field(default_factory=dict)


class ExtractionProvider(Protocol):
    def extract(self, document: Document, kind: str) -> ProviderResponse:
        """Return document values as JSON and optional reasons keyed by JSON Pointer."""


@dataclass(frozen=True)
class UnavailableField:
    pointer: str
    reason: str


@dataclass(frozen=True)
class ExtractionError:
    code: str
    message: str
    pointer: str = ""


@dataclass(frozen=True)
class ExtractionResult:
    status: Literal["complete", "needs_review", "failed"]
    kind: str
    payload: Optional[dict[str, Any]] = None
    unavailable_fields: tuple[UnavailableField, ...] = ()
    errors: tuple[ExtractionError, ...] = ()


def _unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("Duplicate JSON key.")
        result[key] = value
    return result


def _reject_constant(token: str) -> None:
    raise ValueError(f"{token} is outside the JSON number grammar.")


def _pointer(path: Iterable[Union[str, int]]) -> str:
    return "".join("/" + str(part).replace("~", "~0").replace("/", "~1") for part in path)


def _available_payload(
    schema: dict[str, Any], payload: Any, path: tuple[Union[str, int], ...], missing: dict[str, str]
) -> Any:
    """Project out unavailable fields for validation while retaining observed rows."""
    if schema["type"] == "array" and isinstance(payload, list):
        return [
            _available_payload(schema["items"], row, (*path, index), missing)
            for index, row in enumerate(payload)
        ]
    if schema["type"] != "object" or not isinstance(payload, dict):
        return payload

    available = payload.copy()
    for name, field_schema in schema["properties"].items():
        field_path = (*path, name)
        value = payload.get(name)
        if name in schema["required"]:
            reason = _unavailable_reason(field_schema["type"], value)
            if reason:
                missing[_pointer(field_path)] = reason
                available.pop(name, None)
                if isinstance(value, str):
                    payload[name] = None
                continue
        if name in payload:
            available[name] = _available_payload(field_schema, value, field_path, missing)
    return available


def _unavailable_reason(field_type: str, value: Any) -> Optional[str]:
    if value is None:
        return "The provider did not supply a value."
    if field_type == "string" and isinstance(value, str) and not value.strip():
        return "The provider supplied a blank value."
    if field_type == "array" and isinstance(value, list) and not value:
        return "The provider supplied no rows."
    return None


def _failed(kind: str, code: str, message: str) -> ExtractionResult:
    return ExtractionResult("failed", kind, errors=(ExtractionError(code, message),))


class ExtractionPipeline:
    def __init__(self, provider: ExtractionProvider, *, max_bytes: int = DEFAULT_MAX_BYTES):
        if type(max_bytes) is not int or max_bytes < 1:
            raise ValueError("max_bytes must be a positive integer.")
        self._provider = provider
        self._max_bytes = max_bytes
        self._validators = {}
        for kind, path in SCHEMA_FILES.items():
            schema = json.loads(path.read_text(encoding="utf-8"))
            Draft202012Validator.check_schema(schema)
            self._validators[kind] = Draft202012Validator(
                schema, format_checker=Draft202012Validator.FORMAT_CHECKER
            )

    def run(self, path: Union[str, Path], kind: str) -> ExtractionResult:
        validator = self._validators.get(kind)
        if validator is None:
            return _failed(kind, "unsupported_kind", "Use quotation or title_deed as the document kind.")
        try:
            document = load_document(Path(path), self._max_bytes)
        except InputError as error:
            return _failed(kind, error.code, str(error))

        try:
            response = self._provider.extract(document, kind)
        except ProviderError as error:
            return _failed(kind, "provider_error", str(error))
        except Exception:
            return _failed(kind, "provider_error", "The extraction provider could not finish the request.")

        if not isinstance(response, ProviderResponse) or not isinstance(response.payload_json, str):
            return _failed(kind, "invalid_provider_response", "The provider must return JSON text.")
        try:
            payload = json.loads(response.payload_json, object_pairs_hook=_unique_object,
                                 parse_float=Decimal, parse_constant=_reject_constant)
        except (ValueError, DecimalException, RecursionError):
            return _failed(kind, "invalid_json", "The provider returned invalid or ambiguous JSON.")

        missing = {}
        available = _available_payload(validator.schema, payload, (), missing)
        violations = tuple(sorted(
            (ExtractionError("schema_violation", f"Canonical schema rule '{error.validator}' failed.",
                             _pointer(error.absolute_path))
             for error in validator.iter_errors(available)
             if not (missing and error.validator == "required")),
            key=lambda error: (error.pointer, error.message),
        ))
        reasons = response.unavailable_reasons
        if not isinstance(reasons, Mapping) or any(
            pointer not in missing or not isinstance(reason, str) or not reason.strip()
            for pointer, reason in reasons.items()
        ):
            return _failed(kind, "invalid_provider_response",
                           "Unavailable reasons must identify missing fields and contain text.")
        fields = tuple(UnavailableField(pointer, reasons.get(pointer, default))
                       for pointer, default in missing.items())
        if violations:
            return ExtractionResult("failed", kind, unavailable_fields=fields, errors=violations)
        if fields:
            return ExtractionResult("needs_review", kind, payload=payload, unavailable_fields=fields)
        return ExtractionResult("complete", kind, payload=payload)
