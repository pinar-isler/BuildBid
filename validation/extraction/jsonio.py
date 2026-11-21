"""Strict JSON parsing and decimal-preserving output for extraction results."""

from decimal import Decimal
import json


def _unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("Duplicate JSON key.")
        result[key] = value
    return result


def _reject_constant(token):
    raise ValueError(f"{token} is outside the JSON number grammar.")


def _check_unicode(text: str) -> None:
    try:
        text.encode("utf-8")
    except UnicodeEncodeError as error:
        raise ValueError("JSON strings must contain valid Unicode.") from error


def _check_strings(value) -> None:
    if isinstance(value, str):
        _check_unicode(value)
    elif isinstance(value, dict):
        for key, item in value.items():
            _check_unicode(key)
            _check_strings(item)
    elif isinstance(value, list):
        for item in value:
            _check_strings(item)


def loads_json(text):
    value = json.loads(text, object_pairs_hook=_unique_object, parse_float=Decimal,
                       parse_constant=_reject_constant)
    _check_strings(value)
    return value


def _dump_object_key(key) -> str:
    if not isinstance(key, str):
        raise ValueError("JSON object keys must be strings.")
    _check_unicode(key)
    return json.dumps(key)


def dumps_json(value) -> str:
    if isinstance(value, Decimal):
        if not value.is_finite():
            raise ValueError("Non-finite decimal is outside the JSON number grammar.")
        return str(value)
    if isinstance(value, dict):
        return "{" + ", ".join(f"{_dump_object_key(key)}: {dumps_json(item)}" for key, item in value.items()) + "}"
    if isinstance(value, (list, tuple)):
        return "[" + ", ".join(dumps_json(item) for item in value) + "]"
    if isinstance(value, str):
        _check_unicode(value)
    return json.dumps(value, allow_nan=False)
