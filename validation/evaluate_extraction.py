"""Report extraction quality from saved live predictions or run a mock smoke test."""

import argparse
from decimal import Decimal, DecimalException, localcontext
from hashlib import sha256
from pathlib import Path
from time import perf_counter_ns

from .extraction.jsonio import dumps_json, loads_json
from .extraction.mock import MockProvider
from .extraction.pipeline import (SCHEMA_FILES, ExtractionError, ExtractionPipeline, ExtractionResult,
                                  ProviderResponse, load_validators, validate_response)
from .extraction_metrics import REPORT_DECIMAL_CONTEXT, rate, score_complete, score_incomplete, summarize_quality


CORPUS = Path(__file__).resolve().parent / "corpus" / "v1"
SPLITS = ("development", "held-out")
TOKEN_FIELDS = ("inputTokens", "outputTokens", "totalTokens")
TELEMETRY_FIELDS = ("latencyMs", *TOKEN_FIELDS, "costUsd")
MOCK_PROVIDERS = {"mock", "fixturereplay", "mockprovider", "mockresponseprovider"}


def _is_mock_provider(name: str) -> bool:
    label = name.rsplit(".", 1)[-1].casefold()
    return "".join(character for character in label if character.isalnum()) in MOCK_PROVIDERS


def _records(bundle, known_ids):
    if not isinstance(bundle, dict) or bundle.get("source") not in ("live", "mock"):
        raise ValueError("A prediction bundle must declare source as live or mock.")
    records = bundle.get("predictions")
    if not isinstance(records, dict):
        raise ValueError("A prediction bundle needs a predictions object keyed by fixture ID.")
    if records.keys() - known_ids:
        unknown = ", ".join(dumps_json(identifier) for identifier in sorted(records.keys() - known_ids))
        raise ValueError(f"Unknown fixture IDs in predictions: {unknown}.")
    source = bundle["source"]
    for key in ("provider", "model"):
        if key in bundle and (not isinstance(bundle[key], str) or not bundle[key].strip()):
            raise ValueError(f"{key} must be a nonempty string when supplied.")
    if source == "live" and _is_mock_provider(bundle.get("provider", "")):
        raise ValueError("Mock providers require a mock prediction bundle.")
    validated = {}
    for fixture, record in records.items():
        if not isinstance(record, dict) or record.get("source", source) != source:
            raise ValueError(f"Prediction {fixture} must be an object from the declared source.")
        for key in ("provider", "model"):
            if key in record and (key not in bundle or record[key] != bundle[key]):
                raise ValueError(f"Prediction {fixture} has a {key} that differs from the bundle declaration.")
        if record.get("payloadJson") is not None and not isinstance(record["payloadJson"], str):
            raise ValueError(f"Prediction {fixture} needs raw JSON text or null in payloadJson.")
        validated[fixture] = (record, _telemetry(record, fixture))
    return validated


def _telemetry(record, fixture):
    tracked = {}
    for field in TELEMETRY_FIELDS:
        value = record.get(field)
        if value is None:
            continue
        message = f"Prediction {fixture} has an invalid {field}."
        if field in TOKEN_FIELDS:
            if type(value) is not int or value < 0:
                raise ValueError(message)
        else:
            if type(value) not in (int, float, Decimal):
                raise ValueError(message)
            value = Decimal(str(value))
            if not value.is_finite() or value < 0:
                raise ValueError(message)
        tracked[field] = value
    return tracked


def _from_record(validator, kind, prediction):
    if prediction is None:
        error = ExtractionError("missing_prediction", "The fixture has no recorded prediction.")
        return ExtractionResult("failed", kind, errors=(error,)), {}
    record, tracked = prediction
    text = record.get("payloadJson")
    if text is None:
        error = ExtractionError("no_response", "The recorded attempt produced no response text.")
        return ExtractionResult("failed", kind, errors=(error,)), tracked
    response = ProviderResponse(text, record.get("unavailableReasons", {}))
    return validate_response(kind, response, validator), tracked


def _operations(documents):
    latency = sorted(doc["telemetry"]["latencyMs"] for doc in documents if "latencyMs" in doc["telemetry"])
    count = len(latency)
    statuses = {status: sum(doc["status"] == status for doc in documents)
                for status in ("complete", "needs_review", "failed")}

    def total(field):
        observed = [doc["telemetry"][field] for doc in documents if field in doc["telemetry"]]
        return {"observedDocuments": len(observed), "total": sum(observed) if observed else None}

    with localcontext(REPORT_DECIMAL_CONTEXT):
        return {
            "documents": len(documents),
            "statuses": statuses,
            "invalidJsonRate": rate(sum("invalid_json" in doc["errorCodes"] for doc in documents), len(documents)),
            "missingPredictions": sum("missing_prediction" in doc["errorCodes"] for doc in documents),
            "noResponses": sum("no_response" in doc["errorCodes"] for doc in documents),
            "latencyMs": {"observedDocuments": count, "mean": sum(latency) / count if count else None,
                          "p50": latency[(count + 1) // 2 - 1] if count else None,
                          "p95": latency[(95 * count + 99) // 100 - 1] if count else None},
            "tokens": {field: total(field) for field in TOKEN_FIELDS},
            "apiCostUsd": total("costUsd"),
        }


def _summary(documents, source):
    return {"operations": _operations(documents),
            "quality": summarize_quality(documents) if source == "live" else None}


def evaluate(split: str = "both", bundle=None) -> dict:
    """Evaluate one saved run, or run the corpus mock when no bundle is supplied."""
    if split not in (*SPLITS, "both"):
        raise ValueError("Choose development, held-out, or both.")
    manifest_bytes = (CORPUS / "manifest.json").read_bytes()
    manifest = loads_json(manifest_bytes)
    known_ids = {doc["id"] for group in manifest["splits"].values() for doc in group["documents"]}
    records = _records(bundle, known_ids) if bundle is not None else {}
    source = bundle["source"] if bundle is not None else "mock"
    mock = ExtractionPipeline(MockProvider()) if bundle is None else None
    validators = load_validators() if bundle is not None else {}
    documents = []
    for split_name in SPLITS if split == "both" else (split,):
        for entry in manifest["splits"][split_name]["documents"]:
            kind = entry["kind"]
            identifier = entry["id"]
            record = records.get(identifier)
            if mock is not None:
                start = perf_counter_ns()
                result = mock.run(CORPUS / split_name / "documents" / entry["file"], kind)
                elapsed = perf_counter_ns() - start
                with localcontext(REPORT_DECIMAL_CONTEXT):
                    tracked = {"latencyMs": Decimal(elapsed) / 1_000_000}
            else:
                result, tracked = _from_record(validators[kind], kind, record)
            doc = {
                "fixtureId": identifier, "split": split_name, "documentKind": kind,
                "sourceFormat": Path(entry["file"]).suffix[1:], "expectedOutcome": entry["outcome"],
                "status": result.status, "errorCodes": sorted({error.code for error in result.errors}),
                "unavailableFieldPointers": [field.pointer for field in result.unavailable_fields],
                "telemetry": tracked,
            }
            if source == "live":
                validator = validators[kind]
                if entry["outcome"] == "complete":
                    gold_path = CORPUS / split_name / "ground-truth" / f"{identifier}.json"
                    gold = loads_json(gold_path.read_text(encoding="utf-8"))
                    validator.validate(gold)
                    text = record[0].get("payloadJson") if record is not None else None
                    doc["canonicalSchemaValid"] = (text is not None and "invalid_json" not in doc["errorCodes"]
                                                   and validator.is_valid(loads_json(text)))
                    doc["quality"] = score_complete(gold, result, validator.schema)
                else:
                    doc["quality"] = score_incomplete(entry, result)
            documents.append(doc)

    breakdowns = {}
    for field in ("split", "documentKind", "sourceFormat"):
        values = sorted({doc[field] for doc in documents})
        breakdowns[field] = {value: _summary([doc for doc in documents if doc[field] == value], source)
                             for value in values}
    return {
        "source": source,
        "provider": "fixture_replay" if bundle is None else bundle.get("provider"),
        "model": None if bundle is None else bundle.get("model"),
        "corpusVersion": manifest["version"], "split": split,
        "independentEvaluation": False,
        "manifestSha256": sha256(manifest_bytes).hexdigest(),
        "schemaSha256": {kind: sha256(path.read_bytes()).hexdigest() for kind, path in SCHEMA_FILES.items()},
        "latencySource": "local_mock_pipeline" if bundle is None else "recorded_prediction_metadata",
        **_summary(documents, source),
        "breakdowns": breakdowns,
        "documents": documents,
    }


def _format_rate(metric) -> str:
    fraction = f"{metric['count']}/{metric['total']}"
    return fraction if metric["rate"] is None else f"{fraction} ({metric['rate']:.1%})"


def _format_value(value) -> str:
    return "unavailable" if value is None else str(value)


def _format_label(value) -> str:
    if value is not None and not value.isprintable():
        return dumps_json(value)
    return _format_value(value)


def console_report(report: dict) -> str:
    operations = report["operations"]
    source = report["source"]
    latency = operations["latencyMs"]
    quality = report["quality"]
    lines = [f"BuildBid extraction benchmark — {source} — {report['split']}",
             "Statuses  " + "  ".join(f"{key} {value}" for key, value in operations["statuses"].items()),
             f"Invalid JSON  {_format_rate(operations['invalidJsonRate'])}",
             f"Missing predictions  {operations['missingPredictions']}",
             f"Attempts without response text  {operations['noResponses']}",
             f"Latency in ms ({report['latencySource']}, {latency['observedDocuments']} documents)  "
             f"mean {_format_value(latency['mean'])}  p50 {_format_value(latency['p50'])}  p95 {_format_value(latency['p95'])}"]
    if report["provider"] or report["model"]:
        lines.insert(1, f"Provider {_format_label(report['provider'])}  Model {_format_label(report['model'])}")
    for field, totals in operations["tokens"].items():
        lines.append(f"{field}  {_format_value(totals['total'])}  ({totals['observedDocuments']} documents)")
    costs = operations["apiCostUsd"]
    lines.append(f"Recorded API cost in USD  {_format_value(costs['total'])}  ({costs['observedDocuments']} documents)")
    if quality is None:
        lines.append("Mock replay exercises the pipeline. Extraction quality metrics are suppressed.")
    else:
        lines.extend(["Quality on corpus v1 fixtures using caller-recorded predictions.",
                      f"Field accuracy  {_format_rate(quality['fieldAccuracy'])}",
                      f"Canonical schema validity on complete cases  {_format_rate(quality['completeSchemaValidRate'])}",
                      f"Missing fields  {quality['fields']['missing']}",
                      f"Incomplete cases passed  {_format_rate(quality['incompleteReviewPassRate'])}"])
        for name in ("fields", "rows"):
            lines.append(f"{name.capitalize()}  " + "  ".join(
                f"{metric} {_format_rate(quality[name][metric])}" for metric in ("precision", "recall", "f1")
            ))
        for group in quality["numericErrors"]:
            lines.append(f"{group['field']} ({group['unit']})  MAE {_format_value(group['meanAbsoluteError'])}  "
                         f"max {_format_value(group['maxAbsoluteError'])}  "
                         f"{group['scoredPairs']}/{group['expectedPairs']} scored pairs")
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser(description="Evaluate recorded BuildBid extraction results offline.")
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--mock", action="store_true", help="Run fixture replay with quality metrics suppressed.")
    source.add_argument("--predictions", type=Path, help="Path to a recorded prediction bundle.")
    parser.add_argument("--split", choices=(*SPLITS, "both"), default="both")
    parser.add_argument("--format", choices=("console", "json"), default="console")
    args = parser.parse_args()
    try:
        bundle = None if args.mock else loads_json(args.predictions.read_text(encoding="utf-8"))
        if args.predictions is not None and bundle is None:
            raise ValueError("A prediction bundle must be a JSON object.")
        report = evaluate(args.split, bundle)
    except DecimalException:
        parser.error("A number exceeds the report's decimal arithmetic range.")
    except (OSError, ValueError, RecursionError) as error:
        parser.error(str(error))
    print(dumps_json(report) if args.format == "json" else console_report(report))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
