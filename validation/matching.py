"""Match extracted quotation items against one project's bill of quantities."""

from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from decimal import Decimal
import re
from typing import Any, Literal, Optional
import unicodedata


MIN_CANDIDATE_SCORE = 0.60
MIN_AUTO_MATCH_SCORE = 0.68
AMBIGUITY_MARGIN = 0.08

_UNIT_ALIASES = {
    alias: unit
    for unit, aliases in {
        "m": ("metre", "meter", "metres", "meters"),
        "m2": ("sqm", "sq m", "metrekare", "square metre", "square metres", "square meter", "square meters"),
        "m3": ("cbm", "cu m", "metrekup", "cubic metre", "cubic metres", "cubic meter", "cubic meters"),
        "kg": ("kilogram", "kilograms", "kgs"),
        "ton": ("t", "tonne", "tonnes"),
        "pcs": ("adet", "ad", "pc", "piece", "pieces"),
        "h": ("saat", "sa", "hr", "hrs", "hour", "hours"),
        "trip": ("sefer", "trips"),
        "week": ("hafta", "weeks"),
        "day": ("gun", "days"),
        "job": ("is", "jobs"),
        "l": ("litre", "liter", "litres", "liters"),
    }.items()
    for alias in (unit, *aliases)
}

_NUMBER = r"\d+(?:[.,]\d+)?"
_LENGTH_UNIT = r"mm|cm|m"
_SIZE = re.compile(rf"(?<!\w)(?<!\d[.,])(?:{_NUMBER}\s*(?:{_LENGTH_UNIT})?\s*x\s*){{1,2}}"
                   rf"{_NUMBER}(?:\s*(?:{_LENGTH_UNIT}))?(?!\w|[.,]\d)")
_SIZE_PART = re.compile(rf"({_NUMBER})\s*({_LENGTH_UNIT})?")
_DIAMETER = re.compile(rf"\b(?:ø|(?:cap|diameter|dia)\s*[:=]?)\s*({_NUMBER})"
                        rf"(?:\s*({_LENGTH_UNIT}))?(?!\w|[.,]\d)"
                        rf"|(?<!\w)(?<!\d[.,])({_NUMBER})\s*({_LENGTH_UNIT})\s+(?:cap|diameter)\b")
_MEASUREMENT = re.compile(rf"(?<!\w)(?<!\d[.,])({_NUMBER})\s*({_LENGTH_UNIT})\b")
_NAMED_MEASUREMENTS = {
    name: re.compile(rf"\b(?:{terms})\b\s*[:=]?\s*({_NUMBER})\s*({_LENGTH_UNIT})\b"
                     rf"|{_MEASUREMENT.pattern}\s+(?:{terms})\b")
    for name, terms in {
        "width": "width|wide|genislik|genisliginde",
        "height": "height|high|yukseklik|yuksekliginde",
        "thickness": "thickness|thick|kalinlik|kalinliginda",
    }.items()
}
_NOMINAL_DIAMETER = re.compile(r"\bdn[\s-]*(\d+)(?:\s*mm)?\b(?![.,]\d)")
_CONCRETE_GRADE = re.compile(r"\bc[\s-]*(\d{2,3})(?:\s*/\s*(\d{2,3}))?\b(?![.,]\d|\s*/\s*\d)")
_STEEL_GRADE = re.compile(r"\b(b\s*\d{3}\s*[abc]|s\s*\d{3}(?:\s*(?:jr|j0|j2|k2))?)\b")
_FIRE_RATING = re.compile(r"\b(rei|ei)\s*[- ]?\s*(\d{2,3})\b")
_APPLICATION = re.compile(r"\b(?:(?P<interior>ic[\s-]+(?:cephe|mekan)|interior|indoor)"
                          r"|(?P<exterior>dis[\s-]+(?:cephe|mekan)|exterior|outdoor)"
                          r"|(?P<roof>cati|roof)|(?P<wall>duvar|wall))\b(?:\s+icin\b)?")
_DESCRIPTION_ALIASES = tuple(
    (re.compile(rf"\b(?:{variants})\b"), term)
    for term, variants in {
        "hazirbeton": r"hazir\s+beton|ready[\s-]+mix(?:ed)?\s+concrete",
        "pompali": r"pompali|pompa\s+ile|pumped|pump[\s-]+placed",
        "drenajborusu": r"drenaj\s+boru(?:su)?|drain(?:age)?\s+pipe",
        "mineralyun": r"mineral\s+(?:yun|wool)",
        "yalitim": r"yalitim(?:i)?|insulation",
        "yanginkapisi": r"yangin\s+kapisi|fire(?:[\s-]+rated)?\s+door",
        "donaticeligi": r"donati\s+celigi|rebar|reinforcing\s+(?:steel|bar)",
    }.items()
)
_MILLIMETRES = {"mm": 1, "cm": 10, "m": 1000}
_TEXT_REPLACEMENTS = str.maketrans({"ı": "i", "×": "x", "⌀": "ø", "∅": "ø", "φ": "ø"})


@dataclass(frozen=True)
class Candidate:
    item_code: str
    description: str
    score: float
    unconfirmed_specs: tuple[str, ...]


@dataclass(frozen=True)
class MatchResult:
    status: Literal["matched", "ambiguous", "unmatched"]
    item_code: Optional[str]
    candidates: tuple[Candidate, ...]
    reason: str


@dataclass(frozen=True)
class _Description:
    text: str
    sorted_text: str
    specs: dict[str, set[str]]


@dataclass(frozen=True)
class _BoqItem:
    code: str
    description: str
    normalized: _Description


def _fold(text: str) -> str:
    folded = unicodedata.normalize("NFKD", text.casefold()).translate(_TEXT_REPLACEMENTS)
    return "".join(character for character in folded if not unicodedata.combining(character))


def normalize_unit(unit: Optional[str]) -> str:
    """Resolve spelling aliases without converting quantities across units."""
    if not isinstance(unit, str):
        return ""
    normalized = " ".join(re.findall(r"[^\W_]+", _fold(unit)))
    if normalized in {"m 2", "m 3"}:
        normalized = normalized.replace(" ", "")
    return _UNIT_ALIASES.get(normalized, normalized)


def levenshtein_distance(left: str, right: str) -> int:
    """Return the edit distance using one row of the dynamic-programming table."""
    if len(left) < len(right):
        left, right = right, left
    previous = list(range(len(right) + 1))
    for row, left_character in enumerate(left, start=1):
        current = [row]
        for column, right_character in enumerate(right, start=1):
            current.append(min(previous[column] + 1, current[-1] + 1,
                               previous[column - 1] + (left_character != right_character)))
        previous = current
    return previous[-1]


def _similarity(left: str, right: str) -> float:
    if left == right:
        return 1.0
    return 1 - levenshtein_distance(left, right) / max(len(left), len(right))


def _number(value: str, unit: Optional[str] = None) -> str:
    decimal = Decimal(value.replace(",", "."))
    if unit:
        decimal *= _MILLIMETRES[unit]
    return format(decimal.normalize(), "f")


def _size(match: re.Match) -> str:
    parts = _SIZE_PART.findall(match[0])
    units = {unit for _, unit in parts if unit}
    default_unit = next(iter(units)) if len(units) == 1 else None
    return "x".join(
        _number(value, unit or default_unit) + ("mm" if unit or default_unit else "")
        for value, unit in parts
    )


def _named_measurement(match: re.Match, default_unit: Optional[str] = None) -> str:
    prefix_value, prefix_unit, suffix_value, suffix_unit = match.groups()
    return _number(prefix_value or suffix_value, prefix_unit or suffix_unit or default_unit)


def _concrete_grade(match: re.Match) -> str:
    return "/".join(str(int(value)) for value in match.groups() if value)


def _normalize_description(description: str) -> _Description:
    text = _fold(description)
    specs: dict[str, set[str]] = {}

    # Compare specifications separately so missing qualifiers do not hide a plausible text match.
    def capture(pattern, category, format_value):
        def replace(match):
            value = format_value(match)
            specs.setdefault(category, set()).add(value)
            return " "

        return pattern.sub(replace, text)

    text = capture(_NOMINAL_DIAMETER, "nominal diameter", lambda match: str(int(match[1])))
    text = capture(_DIAMETER, "diameter", lambda match: _named_measurement(match, "mm"))
    text = capture(_SIZE, "compound dimensions", _size)
    for name, pattern in _NAMED_MEASUREMENTS.items():
        text = capture(pattern, name, _named_measurement)
    text = capture(_MEASUREMENT, "measurement", lambda match: _number(match[1], match[2]))
    text = capture(_CONCRETE_GRADE, "concrete grade", _concrete_grade)
    text = capture(_STEEL_GRADE, "steel grade", lambda match: re.sub(r"\s+", "", match[1]))
    text = capture(_FIRE_RATING, "fire rating", lambda match: match[1] + str(int(match[2])))
    text = capture(_APPLICATION, "application", lambda match: match.lastgroup)
    for pattern, term in _DESCRIPTION_ALIASES:
        text = pattern.sub(term, text)
    tokens = re.findall(r"[^\W_]+(?:[./]\d+)*", text)
    return _Description(" ".join(tokens), " ".join(sorted(tokens)), specs)


def _compatible_grades(left: set[str], right: set[str]) -> bool:
    # A shorthand can agree with a full designation without confirming both strengths.
    def agrees(a, b):
        return a == b or a.startswith(b + "/") or b.startswith(a + "/")

    return all(any(agrees(a, b) for b in right) for a in left) and all(
        any(agrees(a, b) for a in left) for b in right
    )


def _compare_specs(quoted: _Description, boq: _Description) -> Optional[tuple[str, ...]]:
    unconfirmed = []
    for name in sorted(quoted.specs.keys() | boq.specs.keys()):
        left = quoted.specs.get(name, set())
        right = boq.specs.get(name, set())
        if left == right:
            continue
        if left and right and not (left < right or right < left):
            if name != "concrete grade" or not _compatible_grades(left, right):
                return None
        unconfirmed.append(name)
    return tuple(unconfirmed)


class BoqMatcher:
    def __init__(self, boq_rows: Iterable[Mapping[str, Any]]):
        self._by_unit: dict[str, list[_BoqItem]] = {}
        codes = set()
        for row in boq_rows:
            code, description = row["itemCode"], row["description"]
            unit = normalize_unit(row["unitOfMeasure"])
            if not isinstance(code, str) or not code.strip() or code in codes or not unit:
                raise ValueError("Each BOQ row needs a unique code, a unit, and a description.")
            normalized = _normalize_description(description) if isinstance(description, str) else None
            if normalized is None or not normalized.text:
                raise ValueError("Each BOQ row needs a unique code, a unit, and a description.")
            codes.add(code)
            self._by_unit.setdefault(unit, []).append(_BoqItem(code, description, normalized))

    def match_item(self, row: Mapping[str, Any]) -> MatchResult:
        unit = normalize_unit(row.get("unitOfMeasure"))
        description = row.get("rawDescription")
        if not unit or not isinstance(description, str) or not description.strip():
            return MatchResult("unmatched", None, (), "The quotation row needs a description and unit.")
        quoted = _normalize_description(description)
        if not quoted.text:
            return MatchResult("unmatched", None, (), "The quotation row has no usable description.")

        candidates = []
        for item in self._by_unit.get(unit, ()):
            unconfirmed = _compare_specs(quoted, item.normalized)
            if unconfirmed is None:
                continue
            score = max(_similarity(quoted.text, item.normalized.text),
                        _similarity(quoted.sorted_text, item.normalized.sorted_text))
            if score >= MIN_CANDIDATE_SCORE:
                candidates.append(Candidate(item.code, item.description, score, unconfirmed))

        ranked = tuple(sorted(candidates, key=lambda candidate: (-candidate.score, candidate.item_code)))
        if not ranked:
            return MatchResult("unmatched", None, (), "No sufficiently similar BOQ row has compatible units and specifications.")
        best = ranked[0]
        if best.unconfirmed_specs:
            detail = ", ".join(best.unconfirmed_specs)
            return MatchResult("ambiguous", None, ranked, f"The best candidate needs confirmation of {detail}.")
        if len(ranked) > 1 and best.score - ranked[1].score <= AMBIGUITY_MARGIN:
            return MatchResult("ambiguous", None, ranked, "Multiple BOQ candidates have similar scores.")
        if best.score < MIN_AUTO_MATCH_SCORE:
            return MatchResult("ambiguous", None, ranked, "The best candidate falls below the automatic match threshold.")
        return MatchResult("matched", best.item_code, ranked, "The best candidate meets the automatic match rules.")

    def match_quotation(self, quotation: Mapping[str, Any]) -> tuple[MatchResult, ...]:
        return tuple(self.match_item(row) for row in quotation["extractedItems"])
