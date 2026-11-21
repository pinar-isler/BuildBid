"""Report development and regression scores for BOQ matching on corpus v1."""

import argparse
from dataclasses import asdict
import json
from pathlib import Path

from .matching import AMBIGUITY_MARGIN, MIN_AUTO_MATCH_SCORE, MIN_CANDIDATE_SCORE, BoqMatcher


CORPUS = Path(__file__).resolve().parent / "corpus" / "v1"
EVALUATION_PHASES = {"development": "development", "held-out": "post_adjustment_regression"}
SPLITS = tuple(EVALUATION_PHASES)
EXPECTED_STATUS = {"correct": "matched", "ambiguous": "ambiguous", "unmatched": "unmatched"}


def _read_json(path: Path):
    return json.loads(path.read_text(encoding="utf-8"))


def _rate(count: int, total: int) -> dict:
    return {"count": count, "total": total, "rate": count / total if total else None}


def _summarize(rows: list[dict]) -> dict:
    correct = [row for row in rows if row["label"] == "correct"]
    ambiguous = [row for row in rows if row["label"] == "ambiguous"]
    unmatched = [row for row in rows if row["label"] == "unmatched"]
    predictions = [row["prediction"] for row in rows]
    auto_matches = sum(prediction["status"] == "matched" for prediction in predictions)
    manual_reviews = sum(prediction["status"] == "ambiguous" for prediction in predictions)
    correct_links = sum(row["prediction"]["status"] == "matched"
                        and row["prediction"]["item_code"] in row["expectedItemCodes"] for row in correct)
    correct_decisions = sum(
        row["prediction"]["status"] == EXPECTED_STATUS[row["label"]]
        and (row["label"] != "correct" or row["prediction"]["item_code"] in row["expectedItemCodes"])
        for row in rows
    )
    return {
        "top1Accuracy": _rate(correct_links, len(correct)),
        "autoMatchPrecision": _rate(correct_links, auto_matches),
        "autoMatchCoverage": _rate(auto_matches, len(rows)),
        "manualReviewRate": _rate(manual_reviews, len(rows)),
        "ambiguousRoutedForReview": _rate(sum(row["prediction"]["status"] == "ambiguous" for row in ambiguous),
                                          len(ambiguous)),
        "unmatchedLeftUnlinked": _rate(sum(row["prediction"]["item_code"] is None for row in unmatched), len(unmatched)),
        "unmatchedIdentified": _rate(sum(row["prediction"]["status"] == "unmatched" for row in unmatched), len(unmatched)),
        "correctDecisions": _rate(correct_decisions, len(rows)),
    }


def evaluate(split: str) -> dict:
    manifest = _read_json(CORPUS / "manifest.json")
    group = manifest["splits"][split]
    project = group["boqProjectId"]
    matcher = BoqMatcher(_read_json(CORPUS / "master-boq.json")[project])
    gold_dir = CORPUS / split / "ground-truth"
    predictions = {}
    for document in group["documents"]:
        if document["kind"] == "quotation" and document["outcome"] == "complete":
            fixture_id = document["id"]
            quotation = _read_json(gold_dir / f"{fixture_id}.json")
            predictions[fixture_id] = matcher.match_quotation(quotation)

    # Keep all label data on the scoring side of the benchmark.
    labels = _read_json(gold_dir / "reconciliation.json")
    if labels.keys() != predictions.keys():
        raise ValueError("The complete quotation inventory differs from the reconciliation labels.")
    rows = []
    for fixture_id, decisions in predictions.items():
        fixture_labels = labels[fixture_id]
        if sorted(label["itemIndex"] for label in fixture_labels) != list(range(len(decisions))):
            raise ValueError(f"Reconciliation labels do not cover every row of {fixture_id}.")
        for label in fixture_labels:
            index = label["itemIndex"]
            rows.append({
                "fixtureId": fixture_id,
                "itemIndex": index,
                "label": label["label"],
                "expectedItemCodes": label["candidateItemCodes"],
                "prediction": asdict(decisions[index]),
            })
    return {
        "corpusVersion": manifest["version"],
        "split": split,
        "evaluationPhase": EVALUATION_PHASES[split],
        "independentEvaluation": False,
        "boqProject": project,
        "settings": {
            "candidateThreshold": MIN_CANDIDATE_SCORE,
            "autoMatchThreshold": MIN_AUTO_MATCH_SCORE,
            "ambiguityMargin": AMBIGUITY_MARGIN,
        },
        "metrics": _summarize(rows),
        "rows": rows,
    }


def main() -> None:
    parser = argparse.ArgumentParser(description="Report BuildBid BOQ development and regression scores.")
    parser.add_argument("--split", choices=(*SPLITS, "both"), default="both",
                        help="Corpus v1 split. The held-out directory is used for regression checks.")
    args = parser.parse_args()
    result = [evaluate(split) for split in SPLITS] if args.split == "both" else evaluate(args.split)
    print(json.dumps(result, indent=2, ensure_ascii=False))


if __name__ == "__main__":
    main()
