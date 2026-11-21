"""Score validated extraction payloads against the synthetic corpus labels."""

from collections import defaultdict, deque
from decimal import Context, Decimal, MAX_EMAX, MIN_EMIN, ROUND_HALF_EVEN, localcontext
import unicodedata

from .extraction.pipeline import ExtractionResult
from .matching import normalize_unit


ROW_KEYS = {"quotation": ("extractedItems", "rawDescription"), "title_deed": ("shareholders", "nationalId")}
REPORT_DECIMAL_CONTEXT = Context(prec=50, rounding=ROUND_HALF_EVEN, Emax=MAX_EMAX, Emin=MIN_EMIN)


def rate(count: int, total: int) -> dict:
    return {"count": count, "total": total, "rate": count / total if total else None}


def _normalized(field, value):
    if isinstance(value, str):
        normalized = " ".join(unicodedata.normalize("NFC", value).split())
        if field == "unitOfMeasure":
            normalized = normalize_unit(normalized) or normalized
        return normalized or None
    return value


def _align(expected, predicted, key):
    available = defaultdict(deque)
    for index, row in enumerate(predicted):
        identity = _normalized(key, row.get(key))
        if identity is not None:
            available[identity].append((index, row))

    aligned = []
    used = set()
    for row in expected:
        queue = available.get(_normalized(key, row[key]))
        if queue:
            index, match = queue.popleft()
            used.add(index)
            aligned.append(match)
        else:
            aligned.append(None)
    return aligned, [row for index, row in enumerate(predicted) if index not in used]


def _numeric_scores(observations):
    groups = []
    for (field, unit), errors in sorted(observations.items()):
        observed = [abs(error) for error in errors if error is not None]
        groups.append({"field": field, "unit": unit, "expectedPairs": len(errors), "scoredPairs": len(observed),
                       "absoluteErrorSum": sum(observed, Decimal(0)), "maxAbsoluteError": max(observed, default=None)})
    return groups


def score_complete(gold: dict, result: ExtractionResult, schema: dict) -> dict:
    row_key, identity = ROW_KEYS[result.kind]
    prediction = result.payload or {}
    gold_rows = gold[row_key]
    predicted_rows = prediction.get(row_key) or []
    aligned, extra_rows = _align(gold_rows, predicted_rows, identity)

    expected_fields = predicted_fields = correct_fields = different_values = 0
    missing = []
    observations = defaultdict(list)

    def compare(fields, expected, actual, prefix=""):
        nonlocal expected_fields, predicted_fields, correct_fields, different_values
        for field in fields:
            expected_fields += 1
            gold_value = _normalized(field, expected[field])
            value = _normalized(field, actual.get(field))
            if value is None:
                missing.append(f"{prefix}/{field}")
            else:
                predicted_fields += 1
                if value == gold_value:
                    correct_fields += 1
                else:
                    different_values += 1

            if field == "totalAreaM2":
                unit, comparable = "m2", True
            elif field == "quantity":
                unit = _normalized("unitOfMeasure", expected["unitOfMeasure"])
                comparable = unit == _normalized("unitOfMeasure", actual.get("unitOfMeasure"))
            elif field == "quotedUnitPrice":
                unit = gold["currency"]
                comparable = (unit == _normalized("currency", prediction.get("currency"))
                              and _normalized("unitOfMeasure", expected["unitOfMeasure"])
                              == _normalized("unitOfMeasure", actual.get("unitOfMeasure")))
            else:
                continue
            error = Decimal(value) - Decimal(gold_value) if value is not None and comparable else None
            observations[(field, unit)].append(error)

    with localcontext(REPORT_DECIMAL_CONTEXT):
        compare([field for field in schema["required"] if field != row_key], gold, prediction)
        row_fields = schema["properties"][row_key]["items"]["required"]
        for index, (expected, actual) in enumerate(zip(gold_rows, aligned)):
            compare(row_fields, expected, actual or {}, f"/{row_key}/{index}")
        numeric = _numeric_scores(observations)
    for row in extra_rows:
        predicted_fields += sum(_normalized(field, row.get(field)) is not None for field in row_fields)

    return {
        "fields": {"expected": expected_fields, "predicted": predicted_fields, "correct": correct_fields,
                   "differentValues": different_values, "missing": len(missing)},
        "rows": {"expected": len(gold_rows), "predicted": len(predicted_rows),
                 "aligned": len(predicted_rows) - len(extra_rows)},
        "missingFieldPointers": missing,
        "numeric": numeric,
    }


def _unset_at_pointer(payload, pointer: str) -> bool:
    *parents, field = (part.replace("~1", "/").replace("~0", "~") for part in pointer[1:].split("/"))
    value = payload
    for part in parents:
        if isinstance(value, dict):
            value = value.get(part)
        elif isinstance(value, list) and part.isdecimal() and int(part) < len(value):
            value = value[int(part)]
        else:
            return False
    return isinstance(value, dict) and value.get(field) is None


def score_incomplete(entry: dict, result: ExtractionResult) -> dict:
    expected = {field["pointer"] for field in entry["unavailableFields"]}
    flagged = {field.pointer for field in result.unavailable_fields}
    row_key, _ = ROW_KEYS[result.kind]
    payload = result.payload
    rows = (payload or {}).get(row_key) or []
    unset = [pointer for pointer in sorted(expected) if _unset_at_pointer(payload, pointer)]

    return {
        "passed": (result.status == "needs_review" and len(rows) == entry["sourceRowCount"]
                   and flagged == expected and set(unset) == expected),
        "expectedRows": entry["sourceRowCount"],
        "predictedRows": len(rows),
        "expectedUnavailablePointers": sorted(expected),
        "unflaggedPointers": sorted(expected - flagged),
        "unexpectedPointers": sorted(flagged - expected),
        "unsetOnRetainedRows": unset,
    }


def _prf(correct: int, predicted: int, expected: int) -> dict:
    return {"precision": rate(correct, predicted), "recall": rate(correct, expected),
            "f1": rate(2 * correct, predicted + expected)}


def _summarize_numeric(complete: list[dict]) -> list[dict]:
    numeric = {}
    for doc in complete:
        for item in doc["quality"]["numeric"]:
            key = (item["field"], item["unit"])
            group = numeric.setdefault(key, {"field": key[0], "unit": key[1], "scoredPairs": 0, "expectedPairs": 0,
                                             "absoluteErrorSum": Decimal(0), "maxAbsoluteError": None})
            for count_key in ("expectedPairs", "scoredPairs", "absoluteErrorSum"):
                group[count_key] += item[count_key]
            if item["maxAbsoluteError"] is not None:
                group["maxAbsoluteError"] = max(group["maxAbsoluteError"] or Decimal(0), item["maxAbsoluteError"])

    errors = []
    for key in sorted(numeric):
        group = numeric[key]
        count = group["scoredPairs"]
        group["meanAbsoluteError"] = group["absoluteErrorSum"] / count if count else None
        errors.append(group)
    return errors


def summarize_quality(documents: list[dict]) -> dict:
    complete = [doc for doc in documents if doc["expectedOutcome"] == "complete"]
    incomplete = [doc for doc in documents if doc["expectedOutcome"] == "incomplete"]
    fields = dict.fromkeys(("expected", "predicted", "correct", "differentValues", "missing"), 0)
    rows = dict.fromkeys(("expected", "predicted", "aligned"), 0)
    for doc in complete:
        score = doc["quality"]
        for key in fields:
            fields[key] += score["fields"][key]
        for key in rows:
            rows[key] += score["rows"][key]
    with localcontext(REPORT_DECIMAL_CONTEXT):
        errors = _summarize_numeric(complete)

    return {
        "completeSchemaValidRate": rate(sum(doc["canonicalSchemaValid"] for doc in complete), len(complete)),
        "fieldAccuracy": rate(fields["correct"], fields["expected"]),
        "fields": {**fields, **_prf(fields["correct"], fields["predicted"], fields["expected"])},
        "rows": {**rows, "missing": rows["expected"] - rows["aligned"], "extra": rows["predicted"] - rows["aligned"],
                 **_prf(rows["aligned"], rows["predicted"], rows["expected"])},
        "numericErrors": errors,
        "incompleteReviewPassRate": rate(sum(doc["quality"]["passed"] for doc in incomplete), len(incomplete)),
    }
