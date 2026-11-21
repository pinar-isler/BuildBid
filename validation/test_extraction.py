"""Offline tests for the local extraction pipeline and CLI."""

from base64 import b64decode
from contextlib import redirect_stdout
from decimal import Decimal
from io import StringIO
import json
import os
from pathlib import Path
import subprocess
import sys
from tempfile import TemporaryDirectory
import unittest
from unittest.mock import patch

from validation.extraction.__main__ import main as cli_main
from validation.extraction.mock import MockProvider
from validation.extraction.pipeline import ExtractionPipeline, ProviderResponse


ROOT = Path(__file__).resolve().parents[1]
CORPUS = ROOT / "validation" / "corpus" / "v1"
DEV = CORPUS / "development" / "documents"
HELD_OUT = CORPUS / "held-out" / "documents"
QUOTE_PDF = DEV / "Q-DEV-01.pdf"
QUOTE_PNG = DEV / "Q-DEV-02.png"

# A 1 by 2 grayscale JPEG with application metadata removed.
JPEG = b64decode(
    "/9j/wAALCAACAAEBAREA/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL/8QAtRAAAgED"
    "AwIEAwUFBAQAAAF9AQIDAAQRBRIhMUEGE1FhByJxFDKBkaEII0KxwRVS0fAkM2JyggkKFhcYGRol"
    "JicoKSo0NTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqDhIWGh4iJipKTlJWW"
    "l5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uHi4+Tl5ufo6erx8vP09fb3"
    "+Pn6/9sAQwACAgICAgIDAgIDBQMDAwUGBQUFBQYIBgYGBgYICggICAgICAoKCgoKCgoKDAwMDAwM"
    "Dg4ODg4PDw8PDw8PDw8P/90ABAAB/9oACAEBAAA/APx5/wCGqf2n/wDor/jH/wAKDUP/AI/X/9k="
)


def quotation():
    return {
        "vendorName": "Sample vendor",
        "quotationDate": "2026-05-19",
        "currency": "TRY",
        "extractedItems": [{
            "rawDescription": "A sample row",
            "quantity": 2,
            "unitOfMeasure": "m3",
            "quotedUnitPrice": 0,
            "isTaxIncluded": False,
        }],
    }


class StubProvider:
    def __init__(self, response):
        self.response = response
        self.calls = []

    def extract(self, document, kind):
        self.calls.append((document, kind))
        return self.response


class ExtractionTests(unittest.TestCase):
    def setUp(self):
        self.temp = TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.work = Path(self.temp.name)

    def write(self, filename, content):
        path = self.work / filename
        path.write_bytes(content)
        return path

    def from_provider(self, payload, reasons=None):
        provider = StubProvider(ProviderResponse(json.dumps(payload), reasons if reasons is not None else {}))
        return ExtractionPipeline(provider).run(QUOTE_PDF, "quotation")

    def cli(self, path, kind, *options):
        proc = subprocess.run([sys.executable, "-B", "-m", "validation.extraction", str(path),
                               "--kind", kind, *map(str, options)], cwd=ROOT,
                              capture_output=True, text=True, check=False)
        self.assertEqual(proc.stderr, "")
        return proc.returncode, json.loads(proc.stdout, parse_float=Decimal)

    def test_mock_replays_all_complete_and_incomplete_fixtures(self):
        manifest = json.loads((CORPUS / "manifest.json").read_text(encoding="utf-8"))
        pipeline = ExtractionPipeline(MockProvider())
        for split, group in manifest["splits"].items():
            for entry in group["documents"]:
                with self.subTest(fixture=entry["id"]):
                    source = CORPUS / split / "documents" / entry["file"]
                    result = pipeline.run(source, entry["kind"])
                    self.assertEqual(result.errors, ())
                    if entry["outcome"] == "complete":
                        gold_path = CORPUS / split / "ground-truth" / (entry["id"] + ".json")
                        gold = json.loads(gold_path.read_text(encoding="utf-8"), parse_float=Decimal)
                        self.assertEqual(result.status, "complete")
                        self.assertEqual(result.payload, gold)
                        self.assertEqual(result.unavailable_fields, ())
                    else:
                        self.assertEqual(result.status, "needs_review")
                        self.assertEqual([(field.pointer, field.reason) for field in result.unavailable_fields],
                                         [(field["pointer"], field["reason"]) for field in entry["unavailableFields"]])
                        for field in result.unavailable_fields:
                            _, array, index, key = field.pointer.split("/")
                            self.assertEqual(len(result.payload[array]), entry["sourceRowCount"])
                            self.assertIsNone(result.payload[array][int(index)][key])

    def test_mock_matches_by_bytes_and_kind(self):
        pipeline = ExtractionPipeline(MockProvider())
        renamed = self.write("opaque.PDF", QUOTE_PDF.read_bytes())
        self.assertEqual(pipeline.run(renamed, "quotation"), pipeline.run(QUOTE_PDF, "quotation"))
        jpeg = self.write("unknown.jpeg", JPEG)
        for path, kind in [(jpeg, "quotation"), (renamed, "title_deed")]:
            with self.subTest(path=path.name, kind=kind):
                result = pipeline.run(path, kind)
                self.assertEqual(result.status, "failed")
                self.assertEqual(result.errors[0].code, "provider_error")
                self.assertIsNone(result.payload)

    def test_injected_provider_receives_validated_media(self):
        provider = StubProvider(ProviderResponse(json.dumps(quotation())))
        pipeline = ExtractionPipeline(provider)
        for path, media_type in [(QUOTE_PDF, "application/pdf"), (QUOTE_PNG, "image/png"),
                                 (self.write("input.JPG", JPEG), "image/jpeg"),
                                 (self.write("input.jpeg", JPEG), "image/jpeg")]:
            with self.subTest(variant=path.name):
                result = pipeline.run(path, "quotation")
                document, kind = provider.calls[-1]
                self.assertEqual(result.status, "complete")
                self.assertEqual(document.content, path.read_bytes())
                self.assertEqual(document.media_type, media_type)
                self.assertEqual(kind, "quotation")

    def test_invalid_inputs_stop_before_provider(self):
        provider = StubProvider(ProviderResponse("{}"))
        pipeline = ExtractionPipeline(provider)
        corrupt_png = bytearray(QUOTE_PNG.read_bytes())
        corrupt_png[29] ^= 1
        directory = self.work / "directory.pdf"
        directory.mkdir()
        cases = [
            (self.write("unknown.txt", b"test"), "unsupported_format"),
            (self.work / "missing.pdf", "input_unreadable"),
            (directory, "input_unreadable"),
            (self.write("empty.png", b""), "invalid_media"),
            (self.write("renamed.pdf", QUOTE_PNG.read_bytes()), "invalid_media"),
            (self.write("truncated.pdf", QUOTE_PDF.read_bytes()[:-30]), "invalid_media"),
            (self.write("damaged.png", corrupt_png), "invalid_media"),
            (self.write("truncated.jpg", JPEG[:-2]), "invalid_media"),
            (self.write("markers.jpeg", b"\xff\xd8\xff\xd9"), "invalid_media"),
        ]
        for path, code in cases:
            with self.subTest(path=path.name):
                result = pipeline.run(path, "quotation")
                self.assertEqual(result.status, "failed")
                self.assertEqual(result.errors[0].code, code)
        self.assertEqual(pipeline.run(QUOTE_PDF, "other").errors[0].code, "unsupported_kind")
        limited = ExtractionPipeline(provider, max_bytes=len(JPEG) - 1)
        self.assertEqual(limited.run(self.write("oversize.jpg", JPEG), "quotation").errors[0].code,
                         "input_too_large")
        self.assertEqual(provider.calls, [])

    def test_missing_values_preserve_rows_and_valid_zero_and_false(self):
        payload = quotation()
        first_row = payload["extractedItems"][0]
        second_row = dict(first_row, quotedUnitPrice=None)
        del first_row["unitOfMeasure"]
        payload["extractedItems"].append(second_row)
        reason = "A dash appears in the price column."
        result = self.from_provider(payload, {"/extractedItems/1/quotedUnitPrice": reason})

        self.assertEqual(result.status, "needs_review")
        self.assertEqual(result.payload, payload)
        self.assertEqual([field.pointer for field in result.unavailable_fields],
                         ["/extractedItems/0/unitOfMeasure", "/extractedItems/1/quotedUnitPrice"])
        self.assertEqual(result.unavailable_fields[1].reason, reason)

    def test_blank_strings_and_empty_row_lists_require_review(self):
        payload = dict(quotation(), vendorName=" \t", extractedItems=[])
        result = self.from_provider(payload)
        self.assertEqual(result.status, "needs_review")
        self.assertIsNone(result.payload["vendorName"])
        self.assertEqual(result.payload["extractedItems"], [])
        self.assertEqual([field.pointer for field in result.unavailable_fields],
                         ["/vendorName", "/extractedItems"])

    def test_provider_cannot_flag_a_present_value_or_invent_a_pointer(self):
        for reason in [{"/currency": "Unknown."}, {"/unknown": "Unknown."}, {"/vendorName": " "}]:
            with self.subTest(reason=reason):
                result = self.from_provider(dict(quotation(), vendorName=None), reason)
                self.assertEqual(result.status, "failed")
                self.assertEqual(result.errors[0].code, "invalid_provider_response")

    def test_canonical_validation_checks_present_values_even_when_other_fields_are_missing(self):
        for key, value, pointer in [
            ("quotationDate", "2026-02-30", "/quotationDate"),
            ("currency", "GBP", "/currency"),
            ("extractedItems", [None], "/extractedItems/0"),
            ("unexpected", None, ""),
        ]:
            with self.subTest(key=key):
                payload = dict(quotation(), vendorName=None)
                payload[key] = value
                result = self.from_provider(payload)
                self.assertEqual(result.status, "failed")
                self.assertIsNone(result.payload)
                self.assertEqual(result.errors[0].code, "schema_violation")
                self.assertEqual(result.errors[0].pointer, pointer)
                self.assertEqual(result.unavailable_fields[0].pointer, "/vendorName")

    def test_canonical_schema_rejects_numeric_cadastral_id_and_negative_quote_price(self):
        title = json.loads((CORPUS / "development" / "ground-truth" / "D-DEV-01.json")
                           .read_text(encoding="utf-8"))
        title["blockAda"] = 124
        deed_provider = StubProvider(ProviderResponse(json.dumps(title)))
        deed = ExtractionPipeline(deed_provider).run(DEV / "D-DEV-01.pdf", "title_deed")
        self.assertEqual(deed.status, "failed")
        self.assertEqual(deed.errors[0].pointer, "/blockAda")

        quote = quotation()
        quote["extractedItems"][0]["quotedUnitPrice"] = -1
        result = self.from_provider(quote)
        self.assertEqual(result.status, "failed")
        self.assertEqual(result.errors[0].pointer, "/extractedItems/0/quotedUnitPrice")

    def test_strict_json_rejects_duplicate_keys_nonfinite_tokens_and_wrappers(self):
        for raw in ['{"x": {"key": 1, "key": 2}}', '{"x": NaN}', '{"x": Infinity}',
                    '```json\n{}\n```', '{"x": }']:
            with self.subTest(raw=raw):
                result = ExtractionPipeline(StubProvider(ProviderResponse(raw))).run(QUOTE_PDF, "quotation")
                self.assertEqual(result.status, "failed")
                self.assertEqual(result.errors[0].code, "invalid_json")

    def test_provider_exception_becomes_a_failure_without_exception_details(self):
        class UnavailableProvider:
            def extract(self, document, kind):
                raise RuntimeError("private configuration details")

        result = ExtractionPipeline(UnavailableProvider()).run(QUOTE_PDF, "quotation")
        self.assertEqual(result.status, "failed")
        self.assertEqual(result.errors[0].code, "provider_error")
        self.assertNotIn("private", result.errors[0].message)

    def test_cli_emits_json_and_exit_status_for_each_outcome(self):
        for path, kind, status, code in [
            (QUOTE_PDF, "quotation", "complete", 0),
            (DEV / "Q-DEV-03.pdf", "quotation", "needs_review", 1),
            (HELD_OUT / "D-HOLD-03.png", "title_deed", "needs_review", 1),
            (self.work / "missing.png", "title_deed", "failed", 2),
        ]:
            with self.subTest(path=path.name):
                exit_code, output = self.cli(path, kind)
                self.assertEqual(exit_code, code)
                self.assertEqual(output["status"], status)
                self.assertEqual(output["documentKind"], kind)
                if status == "complete":
                    self.assertEqual(output["payload"]["extractedItems"][0]["quantity"].as_tuple(),
                                     Decimal("1250.50").as_tuple())

    def test_explicit_response_works_for_unknown_document_and_overrides_fixture(self):
        unknown = self.write("unknown.jpeg", JPEG)
        exit_code, output = self.cli(unknown, "quotation")
        self.assertEqual(exit_code, 2)
        self.assertEqual(output["status"], "failed")
        self.assertEqual(output["errors"][0]["code"], "provider_error")

        payload = quotation()
        response = self.write("response.json", json.dumps(payload).encode())
        for document in (unknown, QUOTE_PDF):
            with self.subTest(document=document.name):
                exit_code, output = self.cli(document, "quotation", "--mock-response", response)
                self.assertEqual(exit_code, 0)
                self.assertEqual(output["status"], "complete")
                self.assertEqual(output["payload"], payload)

    def test_incomplete_explicit_response_uses_review_logic(self):
        payload = quotation()
        payload["extractedItems"][0]["quotedUnitPrice"] = None
        del payload["extractedItems"][0]["unitOfMeasure"]
        response = self.write("partial.json", json.dumps(payload).encode())
        exit_code, output = self.cli(QUOTE_PDF, "quotation", "--mock-response", response)

        self.assertEqual(exit_code, 1)
        self.assertEqual(output["status"], "needs_review")
        self.assertEqual(output["payload"], payload)
        self.assertEqual(output["unavailableFields"], [
            {"pointer": "/extractedItems/0/unitOfMeasure", "reason": "The provider did not supply a value."},
            {"pointer": "/extractedItems/0/quotedUnitPrice", "reason": "The provider did not supply a value."},
        ])

    def test_invalid_explicit_response_uses_strict_json_and_canonical_schema(self):
        bad_date = dict(quotation(), quotationDate="2026-02-30")
        for raw, code, pointer in [
            (b'{"unfinished":', "invalid_json", ""),
            (b'{"vendorName":"First","vendorName":"Second"}', "invalid_json", ""),
            (json.dumps(bad_date).encode(), "schema_violation", "/quotationDate"),
        ]:
            with self.subTest(raw=raw):
                response = self.write("invalid.json", raw)
                exit_code, output = self.cli(QUOTE_PDF, "quotation", "--mock-response", response)
                self.assertEqual(exit_code, 2)
                self.assertEqual(output["status"], "failed")
                self.assertIsNone(output["payload"])
                self.assertEqual(output["errors"][0]["code"], code)
                self.assertEqual(output["errors"][0]["pointer"], pointer)

    def test_explicit_response_handles_missing_or_unreadable_files(self):
        invalid_utf8 = self.write("invalid-encoding.json", b"\xff")
        for response in (self.work / "missing.json", self.work, invalid_utf8):
            with self.subTest(response=response.name):
                exit_code, output = self.cli(QUOTE_PDF, "quotation", "--mock-response", response)
                self.assertEqual(exit_code, 2)
                self.assertEqual(output["status"], "failed")
                self.assertEqual(output["errors"][0]["code"], "provider_error")

    def test_explicit_response_still_requires_a_valid_document(self):
        response = self.write("response.json", json.dumps(quotation()).encode())
        for path, code in [(self.work / "missing.pdf", "input_unreadable"),
                           (self.write("broken.png", b"broken"), "invalid_media")]:
            with self.subTest(document=path.name):
                exit_code, output = self.cli(path, "quotation", "--mock-response", response)
                self.assertEqual(exit_code, 2)
                self.assertEqual(output["status"], "failed")
                self.assertEqual(output["errors"][0]["code"], code)

    def test_both_cli_modes_run_without_network_or_environment_variables(self):
        response = self.write("response.json", json.dumps(quotation()).encode())
        with patch.dict(os.environ, {}, clear=True), \
             patch("socket.socket", side_effect=AssertionError("Network access attempted.")) as sockets, \
             patch("socket.getaddrinfo", side_effect=AssertionError("DNS lookup attempted.")) as dns:
            for options in ([], ["--mock-response", str(response)]):
                with self.subTest(options=options):
                    argv = ["validation.extraction", str(QUOTE_PDF), "--kind", "quotation", *options]
                    with patch.object(sys, "argv", argv), redirect_stdout(StringIO()) as stdout:
                        self.assertEqual(cli_main(), 0)
                    self.assertEqual(json.loads(stdout.getvalue())["status"], "complete")
            sockets.assert_not_called()
            dns.assert_not_called()
