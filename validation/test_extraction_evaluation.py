"""Regression tests for extraction scoring and report provenance."""

from copy import deepcopy
from decimal import Decimal, localcontext
from pathlib import Path
import subprocess
import sys
from tempfile import TemporaryDirectory
import unicodedata
import unittest
from unittest.mock import patch

from validation.evaluate_extraction import CORPUS, console_report, evaluate
from validation.extraction.jsonio import dumps_json, loads_json
from validation.extraction.pipeline import (ExtractionPipeline, ExtractionResult, ProviderResponse,
                                           load_validators, validate_response)
from validation.extraction_metrics import score_complete, score_incomplete, summarize_quality


ROOT = CORPUS.parents[2]


def gold(fixture, split="development"):
    return loads_json((CORPUS / split / "ground-truth" / f"{fixture}.json").read_text(encoding="utf-8"))


class ExtractionScoringTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.validators = load_validators()

    def compare(self, expected, actual, kind="quotation"):
        validator = self.validators[kind]
        result = validate_response(kind, ProviderResponse(dumps_json(actual)), validator)
        self.assertIsNotNone(result.payload, result.errors)
        return score_complete(expected, result, validator.schema)

    def test_scores_aligned_fields_and_counts_missing_extra_and_wrong_values(self):
        expected = gold("Q-DEV-01")
        predicted = deepcopy(expected)
        predicted["vendorName"] = "Incorrect vendor"
        first, dropped, third, fourth = predicted["extractedItems"]
        first["quantity"] += Decimal("0.50")
        first["quotedUnitPrice"] += Decimal("0.25")
        first["unitOfMeasure"] = "m³"
        third["rawDescription"] = f"  {unicodedata.normalize('NFD', third['rawDescription'])}  "
        third["quotedUnitPrice"] = None
        extra = dict(dropped, rawDescription="Extra row")
        predicted["extractedItems"] = [fourth, first, third, extra]

        score = self.compare(expected, predicted)
        self.assertEqual(score["fields"], {"expected": 23, "predicted": 22, "correct": 14,
                                           "differentValues": 3, "missing": 6})
        self.assertEqual(score["rows"], {"expected": 4, "predicted": 4, "aligned": 3})
        self.assertIn("/extractedItems/2/quotedUnitPrice", score["missingFieldPointers"])
        self.assertIn("/extractedItems/1/rawDescription", score["missingFieldPointers"])

        summary = summarize_quality([{"quality": score, "expectedOutcome": "complete", "canonicalSchemaValid": False}])
        self.assertEqual(summary["fieldAccuracy"]["rate"], 14 / 23)
        self.assertEqual(summary["fields"]["precision"]["rate"], 14 / 22)
        self.assertEqual(summary["fields"]["recall"]["rate"], 14 / 23)
        self.assertEqual(summary["fields"]["f1"]["rate"], 28 / 45)
        for metric in ("precision", "recall", "f1"):
            self.assertEqual(summary["rows"][metric]["rate"], 0.75)
        self.assertEqual(summary["rows"]["extra"], 1)
        self.assertEqual(summary["rows"]["missing"], 1)
        numeric = {(row["field"], row["unit"]): row for row in summary["numericErrors"]}
        self.assertEqual(numeric[("quantity", "m3")]["meanAbsoluteError"], Decimal("0.50"))
        self.assertEqual(numeric[("quotedUnitPrice", "TRY")]["meanAbsoluteError"], Decimal("0.125"))
        self.assertEqual(numeric[("quotedUnitPrice", "TRY")]["scoredPairs"], 2)

    def test_duplicate_quote_descriptions_align_in_source_order(self):
        expected = gold("Q-DEV-01")
        first = expected["extractedItems"][0]
        second = dict(first, quantity=first["quantity"] + 2)
        expected["extractedItems"] = [first, second]
        score = self.compare(expected, dict(expected, extractedItems=[second, first]))
        self.assertEqual(score["rows"]["aligned"], 2)
        self.assertEqual(score["fields"]["differentValues"], 2)
        observed = next(group for group in score["numeric"] if group["field"] == "quantity")
        self.assertEqual(observed["absoluteErrorSum"], Decimal(4))

    def test_unaligned_rows_contribute_extra_values_and_missing_gold_fields(self):
        expected = gold("Q-DEV-01")
        expected["extractedItems"] = expected["extractedItems"][:1]
        [first] = expected["extractedItems"]
        predicted = dict(expected, extractedItems=[
            dict(first, rawDescription=first["rawDescription"] + "!"),
            dict(first, rawDescription=None),
        ])
        score = self.compare(expected, predicted)
        self.assertEqual(score["rows"], {"expected": 1, "predicted": 2, "aligned": 0})
        self.assertEqual(score["fields"], {"expected": 8, "predicted": 12, "correct": 3,
                                           "differentValues": 0, "missing": 5})

    def test_zero_and_false_are_present_and_unknown_symbol_unit_is_wrong(self):
        expected = gold("Q-DEV-01")
        first = dict(expected["extractedItems"][0], quantity=0, quotedUnitPrice=0, isTaxIncluded=False)
        expected["extractedItems"] = [first]
        score = self.compare(expected, dict(expected, extractedItems=[dict(first, unitOfMeasure="%")]))
        self.assertEqual(score["fields"], {"expected": 8, "predicted": 8, "correct": 7,
                                           "differentValues": 1, "missing": 0})
        self.assertEqual(score["missingFieldPointers"], [])
        self.assertTrue(all(group["scoredPairs"] == 0 for group in score["numeric"]))

    def test_deed_rows_use_id_instead_of_display_name_and_keep_leading_zeros(self):
        expected = gold("D-HOLD-01", "held-out")
        predicted = deepcopy(expected)
        predicted["shareholders"].reverse()
        predicted["parcelParsel"] = expected["parcelParsel"].lstrip("0")
        predicted["totalAreaM2"] += Decimal("0.125")
        score = self.compare(expected, predicted, "title_deed")
        self.assertEqual(score["rows"]["aligned"], len(expected["shareholders"]))
        self.assertEqual(score["fields"]["differentValues"], 2)
        self.assertEqual(score["numeric"][0]["maxAbsoluteError"], Decimal("0.125"))

    def test_numeric_errors_require_comparable_units_and_currency(self):
        expected = gold("Q-DEV-01")
        predicted = deepcopy(expected)
        predicted["currency"] = "USD"
        predicted["extractedItems"][0]["unitOfMeasure"] = "m2"
        score = self.compare(expected, predicted)
        numeric = {(row["field"], row["unit"]): row for row in score["numeric"]}
        self.assertEqual(numeric[("quantity", "m3")]["scoredPairs"], 0)
        self.assertEqual(numeric[("quotedUnitPrice", "TRY")]["scoredPairs"], 0)
        self.assertEqual(numeric[("quantity", "kg")]["scoredPairs"], 1)
        predicted["currency"] = "TRY"
        score = self.compare(expected, predicted)
        price = next(group for group in score["numeric"] if group["field"] == "quotedUnitPrice")
        self.assertEqual(price["scoredPairs"], 3)

    def test_numeric_summaries_are_weighted_by_scored_pairs(self):
        expected = gold("Q-DEV-01")
        first = expected["extractedItems"][0]
        second = dict(first, rawDescription="Second placement")
        expected["extractedItems"] = [first, second]
        scores = [self.compare(expected, candidate) for candidate in (
            dict(expected, extractedItems=[first, dict(second, quantity=second["quantity"] + 2)]),
            dict(expected, extractedItems=[dict(first, quantity=first["quantity"] - 10)]),
        )]
        summary = summarize_quality([{"quality": score, "expectedOutcome": "complete", "canonicalSchemaValid": True}
                                     for score in scores])
        quantity = next(group for group in summary["numericErrors"] if group["field"] == "quantity")
        self.assertEqual(quantity["expectedPairs"], 4)
        self.assertEqual(quantity["scoredPairs"], 3)
        self.assertEqual(quantity["meanAbsoluteError"], Decimal(4))
        self.assertEqual(quantity["maxAbsoluteError"], Decimal(10))

    def test_incomplete_review_requires_rows_exact_flags_and_unset_values(self):
        payload = gold("Q-DEV-01")
        payload["extractedItems"] = [dict(payload["extractedItems"][0], quotedUnitPrice=None)]
        manifest = loads_json((CORPUS / "manifest.json").read_bytes())
        entry = next(doc for doc in manifest["splits"]["development"]["documents"] if doc["id"] == "Q-DEV-03")
        for predicted, expected_pass in [
            (payload, True),
            (dict(payload, vendorName=None), False),
            (dict(payload, extractedItems=[]), False),
            (dict(payload, extractedItems=[dict(payload["extractedItems"][0], quotedUnitPrice=0)]), False),
        ]:
            with self.subTest(expected_pass=expected_pass, payload=predicted):
                result = validate_response("quotation", ProviderResponse(dumps_json(predicted)),
                                           self.validators["quotation"])
                self.assertEqual(score_incomplete(entry, result)["passed"], expected_pass)

    def test_top_level_missing_field_needs_a_payload_and_retained_rows(self):
        entry = {"unavailableFields": [{"pointer": "/city"}], "sourceRowCount": 1}
        payload = gold("D-DEV-02")
        del payload["city"]
        result = validate_response("title_deed", ProviderResponse(dumps_json(payload)), self.validators["title_deed"])
        self.assertTrue(score_incomplete(entry, result)["passed"])

        score = score_incomplete(entry, ExtractionResult("failed", "title_deed"))
        self.assertFalse(score["passed"])
        self.assertEqual(score["unsetOnRetainedRows"], [])

    def test_no_complete_cases_has_undefined_quality_rates(self):
        summary = summarize_quality([])
        self.assertIsNone(summary["fieldAccuracy"]["rate"])
        self.assertIsNone(summary["incompleteReviewPassRate"]["rate"])
        for group in ("fields", "rows"):
            for metric in ("precision", "recall", "f1"):
                self.assertEqual(summary[group][metric], {"count": 0, "total": 0, "rate": None})


class ExtractionJsonTests(unittest.TestCase):
    def test_strict_json_round_trips_unicode_and_decimal_precision(self):
        precise = Decimal("0.1000000000000000000000000000000500")
        value = {"Ölçü": [precise, loads_json(r'"\ud83d\ude00"'), "\n"]}
        round_trip = loads_json(dumps_json(value))
        self.assertEqual(round_trip, value)
        self.assertEqual(round_trip["Ölçü"][0].as_tuple(), precise.as_tuple())

    def test_strict_json_rejects_nested_duplicates_nonfinite_numbers_and_surrogates(self):
        for invalid in (r'{"a":[{"x":1,"x":2}]}', r'{"x":NaN}', r'{"\ud800":1}', r'["\udc00"]'):
            with self.subTest(invalid=invalid):
                with self.assertRaises(ValueError):
                    loads_json(invalid)
        for invalid in ({1: "value"}, {"x": Decimal("Infinity")}, {"x": float("nan")}, "\ud800"):
            with self.subTest(invalid=invalid):
                with self.assertRaises(ValueError):
                    dumps_json(invalid)

    def test_pipeline_and_saved_response_validation_have_matching_outcomes(self):
        class StaticProvider:
            def __init__(self, response):
                self.response = response

            def extract(self, document, kind):
                return self.response

        validator = load_validators()["quotation"]
        document = CORPUS / "development" / "documents" / "Q-DEV-01.pdf"
        payload = gold("Q-DEV-01")
        for response, status, error in [
            (ProviderResponse(dumps_json(payload)), "complete", None),
            (ProviderResponse(dumps_json(dict(payload, vendorName=" "))), "needs_review", None),
            (ProviderResponse(dumps_json(dict(payload, currency="GBP"))), "failed", "schema_violation"),
            (ProviderResponse(dumps_json(payload), {"/vendorName": "Unexpected."}),
             "failed", "invalid_provider_response"),
            (ProviderResponse(r'{"vendorName":"\ud800"}'), "failed", "invalid_json"),
        ]:
            with self.subTest(status=status, error=error):
                result = ExtractionPipeline(StaticProvider(response)).run(document, "quotation")
                self.assertEqual(result, validate_response("quotation", response, validator))
                self.assertEqual(result.status, status)
                self.assertEqual(result.errors[0].code if result.errors else None, error)


class ExtractionReportTests(unittest.TestCase):
    def test_mock_runs_never_score_quality_even_for_recorded_mock_output(self):
        with patch("validation.evaluate_extraction.score_complete") as complete, \
             patch("validation.evaluate_extraction.score_incomplete") as incomplete:
            report = evaluate()
            recorded = evaluate("development", {"source": "mock", "predictions": {
                "Q-DEV-01": {"payloadJson": dumps_json(gold("Q-DEV-01"))},
            }})
        complete.assert_not_called()
        incomplete.assert_not_called()
        for result in (report, recorded):
            self.assertEqual(result["source"], "mock")
            self.assertIsNone(result["quality"])
            self.assertTrue(all("quality" not in doc for doc in result["documents"]))
            self.assertTrue(all(section["quality"] is None for group in result["breakdowns"].values()
                                for section in group.values()))
            self.assertNotIn("fieldAccuracy", dumps_json(result))
            self.assertNotIn("Field accuracy", console_report(result))
        self.assertEqual(report["operations"]["statuses"], {"complete": 8, "needs_review": 2, "failed": 0})
        self.assertEqual(report["operations"]["latencyMs"]["observedDocuments"], 10)
        self.assertIsNone(report["operations"]["apiCostUsd"]["total"])

    def test_saved_run_counts_failures_and_only_uses_recorded_telemetry(self):
        invalid_schema = dict(gold("Q-DEV-02"), currency="GBP", vendorName="Private raw value")
        # Test input simulates the shape of a saved run and is never a published live result.
        bundle = {"source": "live", "provider": "test-provider", "predictions": {
            "Q-DEV-01": {"payloadJson": '{"duplicate":1,"duplicate":2}', "latencyMs": 300,
                         "inputTokens": 100, "outputTokens": 3, "totalTokens": 103, "costUsd": Decimal("0.001")},
            "Q-DEV-02": {"payloadJson": dumps_json(invalid_schema), "latencyMs": 100, "inputTokens": 200},
            "Q-DEV-03": {"payloadJson": None, "latencyMs": 200, "outputTokens": 0},
            "D-DEV-01": {"payloadJson": dumps_json(gold("D-DEV-01"))},
        }}
        report = evaluate("development", bundle)
        operations = report["operations"]
        self.assertFalse(report["independentEvaluation"])
        self.assertEqual(operations["statuses"], {"complete": 1, "needs_review": 0, "failed": 4})
        self.assertEqual(operations["invalidJsonRate"], {"count": 1, "total": 5, "rate": 0.2})
        self.assertEqual(operations["missingPredictions"], 1)
        self.assertEqual(operations["noResponses"], 1)
        self.assertEqual(operations["latencyMs"], {"observedDocuments": 3, "mean": 200, "p50": 200, "p95": 300})
        self.assertEqual(operations["tokens"]["inputTokens"], {"observedDocuments": 2, "total": 300})
        self.assertEqual(operations["tokens"]["outputTokens"], {"observedDocuments": 2, "total": 3})
        self.assertEqual(operations["tokens"]["totalTokens"], {"observedDocuments": 1, "total": 103})
        self.assertEqual(operations["apiCostUsd"], {"observedDocuments": 1, "total": Decimal("0.001")})
        self.assertEqual(report["quality"]["completeSchemaValidRate"], {"count": 1, "total": 4, "rate": 0.25})
        self.assertEqual(report["quality"]["incompleteReviewPassRate"]["count"], 0)
        self.assertEqual(report["breakdowns"]["documentKind"]["quotation"]["operations"]["documents"], 3)
        self.assertIn("Field accuracy", console_report(report))
        self.assertNotIn("Private raw value", dumps_json(report))

    def test_mixed_sources_and_invalid_bundle_metadata_are_rejected(self):
        for bundle in [
            {"source": "unknown", "predictions": {}},
            {"source": "live", "provider": "MockProvider", "predictions": {}},
            {"source": "live", "provider": "fixture-replay", "predictions": {}},
            {"source": "live", "provider": "validation.extraction.mock.MockProvider", "predictions": {}},
            {"source": "live", "predictions": {"Q-DEV-01": {"source": "mock"}}},
            {"source": "live", "provider": "test-provider", "predictions": {"Q-DEV-01": {"provider": "mock"}}},
            {"source": "live", "model": "test-model", "predictions": {"Q-DEV-01": {"model": "other-model"}}},
            {"source": "live", "predictions": {"Q-DEV-01": {"provider": "mock"}}},
            {"source": "live", "predictions": {"unknown-id": {}}},
            {"source": "live", "predictions": {"Q-DEV-01": {"payloadJson": {}}}},
            {"source": "live", "predictions": {"Q-DEV-01": {"inputTokens": True}}},
            {"source": "live", "predictions": {"Q-DEV-01": {"latencyMs": -1}}},
            {"source": "live", "predictions": {"Q-DEV-01": {"costUsd": Decimal("Infinity")}}},
        ]:
            with self.subTest(bundle=bundle):
                with self.assertRaises(ValueError):
                    evaluate("development", bundle)

    def test_console_metadata_cannot_inject_a_report_line_or_escape_sequence(self):
        report = evaluate("development", {"source": "mock", "provider": "replay\nField accuracy  100%",
                                         "model": "\x1b[2J", "predictions": {}})
        output = console_report(report)
        self.assertNotIn("\nField accuracy", output)
        self.assertNotIn("\x1b", output)
        self.assertIn(r"\nField accuracy", output)
        self.assertIsNone(report["quality"])

    def test_canonical_validity_uses_raw_json_and_quality_uses_pipeline_result(self):
        placeholder = dict(gold("Q-DEV-01"), vendorName=" ")
        bundle = {"source": "live", "predictions": {
            "Q-DEV-01": {"payloadJson": dumps_json(placeholder)},
            "Q-DEV-02": {"payloadJson": dumps_json(gold("Q-DEV-02")),
                         "unavailableReasons": {"/vendorName": "Conflicts with the observed name."}},
        }}
        report = evaluate("development", bundle)
        self.assertEqual(report["quality"]["completeSchemaValidRate"]["count"], 2)
        documents = {doc["fixtureId"]: doc for doc in report["documents"]}
        self.assertEqual(documents["Q-DEV-01"]["status"], "needs_review")
        self.assertEqual(documents["Q-DEV-01"]["quality"]["missingFieldPointers"], ["/vendorName"])
        self.assertEqual(documents["Q-DEV-02"]["status"], "failed")
        self.assertEqual(documents["Q-DEV-02"]["quality"]["fields"]["predicted"], 0)

    def test_empty_live_bundle_counts_all_gold_fields_and_rows_as_missing(self):
        report = evaluate("development", {"source": "live", "predictions": {}})
        operations, quality = report["operations"], report["quality"]
        self.assertEqual(operations["missingPredictions"], 5)
        self.assertEqual(operations["invalidJsonRate"], {"count": 0, "total": 5, "rate": 0.0})
        self.assertEqual(operations["latencyMs"], {"observedDocuments": 0, "mean": None, "p50": None, "p95": None})
        for key in ("fields", "rows"):
            group = quality[key]
            self.assertGreater(group["expected"], 0)
            self.assertEqual(group["missing"], group["expected"])
            self.assertIsNone(group["precision"]["rate"])
            self.assertEqual(group["recall"]["rate"], 0)
            self.assertEqual(group["f1"]["rate"], 0)
        self.assertTrue(all(group["meanAbsoluteError"] is None for group in quality["numericErrors"]))

    def test_recorded_telemetry_preserves_zero_decimal_totals_and_nearest_rank(self):
        bundle = {"source": "live", "predictions": {
            "Q-DEV-01": {"latencyMs": 0, "inputTokens": 0, "costUsd": Decimal("0.1")},
            "Q-DEV-02": {"latencyMs": Decimal("14.25"), "outputTokens": 0, "costUsd": Decimal("0.2")},
            "D-DEV-01": {"totalTokens": 0, "costUsd": 0},
        }}
        operations = evaluate("development", bundle)["operations"]
        self.assertEqual(operations["latencyMs"], {"observedDocuments": 2, "mean": Decimal("7.125"),
                                                   "p50": 0, "p95": Decimal("14.25")})
        self.assertEqual(operations["apiCostUsd"], {"observedDocuments": 3, "total": Decimal("0.3")})
        for tokens in operations["tokens"].values():
            self.assertEqual(tokens, {"observedDocuments": 1, "total": 0})

    def test_report_arithmetic_handles_large_finite_values_and_ambient_context(self):
        huge = Decimal("1e1000000")
        tiny = Decimal("0.00000000000000000000000001")
        bundle = {"source": "live", "predictions": {
            "Q-DEV-01": {"latencyMs": huge, "costUsd": Decimal("1.00000000000000000000000001")},
            "Q-DEV-02": {"costUsd": tiny},
        }}
        with localcontext() as context:
            context.prec = 4
            operations = evaluate("development", bundle)["operations"]
        self.assertEqual(operations["latencyMs"]["mean"], huge)
        self.assertEqual(operations["apiCostUsd"]["total"], Decimal("1.00000000000000000000000002"))

    def test_mock_timing_converts_nanoseconds_to_milliseconds(self):
        clock_ticks = [0, 1_234_567] * 5
        with patch("validation.evaluate_extraction.perf_counter_ns", side_effect=clock_ticks):
            report = evaluate("development")
        self.assertEqual(report["latencySource"], "local_mock_pipeline")
        self.assertTrue(all(doc["telemetry"]["latencyMs"] == Decimal("1.234567") for doc in report["documents"]))
        self.assertEqual(report["operations"]["latencyMs"]["p95"], Decimal("1.234567"))

    def test_cli_emits_json_and_console_from_recorded_and_mock_inputs(self):
        command = [sys.executable, "-B", "-m", "validation.evaluate_extraction", "--split", "development"]
        with TemporaryDirectory() as directory:
            path = Path(directory) / "predictions.json"
            path.write_text(dumps_json({"source": "live", "predictions": {
                "Q-DEV-01": {"payloadJson": "{broken"},
            }}), encoding="utf-8")
            recorded_json = subprocess.check_output([*command, "--predictions", str(path), "--format", "json"],
                                                     cwd=ROOT, text=True)
            recorded = loads_json(recorded_json)
            self.assertEqual(recorded["operations"]["invalidJsonRate"]["count"], 1)
            self.assertEqual(recorded["quality"]["fieldAccuracy"]["count"], 0)

        mock_console = subprocess.check_output([*command, "--mock"], cwd=ROOT, text=True)
        self.assertIn("quality metrics are suppressed", mock_console)
        self.assertNotIn("Field accuracy", mock_console)

    def test_cli_rejects_duplicate_or_nonfinite_bundle_metadata(self):
        with TemporaryDirectory() as directory:
            path = Path(directory) / "predictions.json"
            for text in ('{"source":"mock","source":"live","predictions":{}}',
                         '{"source":"mock","predictions":{"Q-DEV-01":{"latencyMs":NaN}}}'):
                with self.subTest(text=text):
                    path.write_text(text, encoding="utf-8")
                    proc = subprocess.run([sys.executable, "-B", "-m", "validation.evaluate_extraction",
                                           "--predictions", str(path), "--format", "json"],
                                          cwd=ROOT, capture_output=True, text=True)
                    self.assertEqual(proc.returncode, 2)
                    self.assertEqual(proc.stdout, "")
                    self.assertNotIn("Traceback", proc.stderr)
