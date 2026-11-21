"""Exercise provider wire formats and pipeline outcomes with fake HTTP clients."""

from base64 import b64decode
from decimal import Decimal
import json
import os
from pathlib import Path
import unittest
from unittest.mock import patch

from jsonschema import Draft202012Validator

from validation.extraction.media import Document
from validation.extraction.pipeline import ExtractionPipeline, ProviderError
from validation.extraction.providers import GeminiProvider, OpenAIProvider


DEV = Path(__file__).resolve().parent / "corpus" / "v1" / "development"
QUOTATION_PDF = DEV / "documents" / "Q-DEV-01.pdf"
DEED_PNG = DEV / "documents" / "D-DEV-02.png"


class FakeResponse:
    def __init__(self, body, status_code=200):
        self._body = body
        self.status_code = status_code

    def json(self):
        if isinstance(self._body, Exception):
            raise self._body
        return self._body


class FakeHttpClient:
    def __init__(self, response):
        self.response = response
        self.calls = []

    def post(self, url, *, json, timeout):
        self.calls.append((url, json, timeout))
        if isinstance(self.response, Exception):
            raise self.response
        return self.response


def gemini_body(*fragments, finish_reason="STOP"):
    return {"candidates": [{
        "finishReason": finish_reason,
        "content": {"parts": [{"thought": True, "text": "Internal reasoning."}]
                     + [{"text": text} for text in fragments]},
    }]}


def openai_body(*fragments, status="completed"):
    return {"status": status, "output": [
        {"type": "reasoning", "summary": [{"type": "summary_text", "text": "Internal reasoning."}]},
        {"type": "message", "status": "completed", "role": "assistant",
         "content": [{"type": "output_text", "text": text} for text in fragments]},
    ]}


PROVIDERS = [(GeminiProvider, gemini_body), (OpenAIProvider, openai_body)]
MEDIA_TYPES = ("application/pdf", "image/png", "image/jpeg")


class ProviderTests(unittest.TestCase):
    def test_gemini_inline_media_and_structured_request(self):
        for media_type in MEDIA_TYPES:
            with self.subTest(media_type=media_type):
                client = FakeHttpClient(FakeResponse(gemini_body("{", "}")))
                provider = GeminiProvider(client, model="gemini-test", timeout=12.0)
                response = provider.extract(Document(b"fixture bytes", media_type), "quotation")

                self.assertEqual(response.payload_json, "{}")
                [(url, request, timeout)] = client.calls
                self.assertEqual(url, "https://generativelanguage.googleapis.com/v1beta/models/gemini-test:generateContent")
                self.assertEqual(timeout, 12.0)
                [message] = request["contents"]
                self.assertEqual(message["role"], "user")
                prompt, attachment = message["parts"]
                self.assertIn("vendor quotation", prompt["text"])
                self.assertIn("Return null", prompt["text"])
                self.assertEqual(attachment["inlineData"]["mimeType"], media_type)
                self.assertEqual(b64decode(attachment["inlineData"]["data"]), b"fixture bytes")
                config = request["generationConfig"]
                self.assertEqual(config["responseMimeType"], "application/json")
                self.assert_quotation_schema_supports_missing_values(config["responseJsonSchema"])

    def test_openai_pdf_image_and_strict_structured_request(self):
        for media_type in MEDIA_TYPES:
            with self.subTest(media_type=media_type):
                client = FakeHttpClient(FakeResponse(openai_body("{", "}")))
                provider = OpenAIProvider(client, model="gpt-test", timeout=8.0)
                response = provider.extract(Document(b"fixture bytes", media_type), "quotation")

                self.assertEqual(response.payload_json, "{}")
                [(url, request, timeout)] = client.calls
                self.assertEqual(url, "https://api.openai.com/v1/responses")
                self.assertEqual(timeout, 8.0)
                self.assertEqual(request["model"], "gpt-test")
                self.assertIs(request["store"], False)
                [message] = request["input"]
                self.assertEqual(message["role"], "user")
                prompt, attachment = message["content"]
                self.assertEqual(prompt["type"], "input_text")
                self.assertIn("Return null", prompt["text"])
                if media_type == "application/pdf":
                    self.assertEqual(attachment["type"], "input_file")
                    self.assertEqual(attachment["filename"], "document.pdf")
                    data_url = attachment["file_data"]
                else:
                    self.assertEqual(attachment["type"], "input_image")
                    data_url = attachment["image_url"]
                prefix, encoded = data_url.split(",", 1)
                self.assertEqual(prefix, f"data:{media_type};base64")
                self.assertEqual(b64decode(encoded), b"fixture bytes")
                schema_format = request["text"]["format"]
                self.assertEqual(schema_format["type"], "json_schema")
                self.assertEqual(schema_format["name"], "VendorQuotationExtraction")
                self.assertIs(schema_format["strict"], True)
                self.assert_quotation_schema_supports_missing_values(schema_format["schema"])

    def assert_quotation_schema_supports_missing_values(self, schema):
        self.assertNotIn("$schema", schema)
        self.assertEqual(schema["type"], "object")
        self.assertFalse(schema["additionalProperties"])
        self.assertCountEqual(schema["required"], schema["properties"])
        row = schema["properties"]["extractedItems"]["items"]
        self.assertFalse(row["additionalProperties"])
        self.assertCountEqual(row["required"], row["properties"])
        self.assertEqual(schema["properties"]["quotationDate"]["format"], "date")
        self.assertEqual(row["properties"]["quotedUnitPrice"]["minimum"], 0)

        Draft202012Validator.check_schema(schema)
        validator = Draft202012Validator(schema, format_checker=Draft202012Validator.FORMAT_CHECKER)
        payload = {"vendorName": "Vendor", "quotationDate": None, "currency": None, "extractedItems": [{
            "rawDescription": "Concrete", "quantity": 0, "unitOfMeasure": "m3", "quotedUnitPrice": None,
            "isTaxIncluded": False,
        }]}
        self.assertTrue(validator.is_valid(payload))
        self.assertTrue(validator.is_valid(dict(payload, currency="TRY")))
        self.assertTrue(validator.is_valid(dict(payload, extractedItems=None)))
        self.assertFalse(validator.is_valid(dict(payload, currency="GBP")))
        self.assertFalse(validator.is_valid(dict(payload, quotationDate="2026-02-30")))
        self.assertFalse(validator.is_valid(dict(payload, extractedItems=[None])))

    def test_both_adapters_run_without_network_or_credentials(self):
        source = (DEV / "ground-truth" / "Q-DEV-01.json").read_text(encoding="utf-8")
        for adapter, envelope in PROVIDERS:
            with self.subTest(adapter=adapter.__name__):
                client = FakeHttpClient(FakeResponse(envelope(source[:50], source[50:])))
                with patch.dict(os.environ, {}, clear=True), \
                     patch("socket.socket", side_effect=AssertionError("Network access attempted.")) as sockets, \
                     patch("socket.getaddrinfo", side_effect=AssertionError("DNS lookup attempted.")) as dns:
                    result = ExtractionPipeline(adapter(client)).run(QUOTATION_PDF, "quotation")
                sockets.assert_not_called()
                dns.assert_not_called()
                self.assertEqual(result.status, "complete")
                self.assertEqual(result.payload, json.loads(source, parse_float=Decimal))
                self.assertEqual(len(client.calls), 1)

    def test_unavailable_deed_values_keep_the_shareholder_for_review(self):
        payload = json.loads((DEV / "ground-truth" / "D-DEV-02.json").read_text(encoding="utf-8"))
        self.assertEqual(len(payload["shareholders"]), 1)
        payload["shareholders"][0]["nationalId"] = None
        for adapter, envelope in PROVIDERS:
            with self.subTest(adapter=adapter.__name__):
                client = FakeHttpClient(FakeResponse(envelope(json.dumps(payload))))
                result = ExtractionPipeline(adapter(client)).run(DEED_PNG, "title_deed")

                self.assertEqual(result.status, "needs_review")
                self.assertEqual(len(result.payload["shareholders"]), 1)
                self.assertIsNone(result.payload["shareholders"][0]["nationalId"])
                self.assertEqual(result.payload["blockAda"], payload["blockAda"])
                self.assertEqual([field.pointer for field in result.unavailable_fields],
                                 ["/shareholders/0/nationalId"])
                [(_, request, _)] = client.calls
                if adapter is GeminiProvider:
                    schema = request["generationConfig"]["responseJsonSchema"]
                else:
                    schema = request["text"]["format"]["schema"]
                self.assertEqual(schema["title"], "TitleDeedExtraction")
                self.assertTrue(Draft202012Validator(schema).is_valid(payload))

    def test_unavailable_quote_price_keeps_the_line_item_for_review(self):
        payload = json.loads((DEV / "ground-truth" / "Q-DEV-01.json").read_text(encoding="utf-8"))
        payload["extractedItems"][0]["quotedUnitPrice"] = None
        for adapter, envelope in PROVIDERS:
            with self.subTest(adapter=adapter.__name__):
                client = FakeHttpClient(FakeResponse(envelope(json.dumps(payload))))
                result = ExtractionPipeline(adapter(client)).run(QUOTATION_PDF, "quotation")

                self.assertEqual(result.status, "needs_review")
                self.assertEqual(len(result.payload["extractedItems"]), len(payload["extractedItems"]))
                self.assertIsNone(result.payload["extractedItems"][0]["quotedUnitPrice"])
                self.assertEqual([field.pointer for field in result.unavailable_fields],
                                 ["/extractedItems/0/quotedUnitPrice"])

    def test_model_payload_still_uses_strict_json_and_canonical_validation(self):
        source = (DEV / "ground-truth" / "Q-DEV-01.json").read_text(encoding="utf-8")
        invalid_date = dict(json.loads(source), quotationDate="2026-02-30")
        for adapter, envelope in PROVIDERS:
            for payload, expected_code in [("{", "invalid_json"), (json.dumps(invalid_date), "schema_violation")]:
                with self.subTest(adapter=adapter.__name__, code=expected_code):
                    client = FakeHttpClient(FakeResponse(envelope(payload)))
                    result = ExtractionPipeline(adapter(client)).run(QUOTATION_PDF, "quotation")
                    self.assertEqual(result.status, "failed")
                    self.assertEqual(result.errors[0].code, expected_code)

    def test_transport_and_http_errors_are_safe_failures(self):
        for adapter, _ in PROVIDERS:
            for response, expected_message in [
                (TimeoutError("private server details"), "could not be sent"),
                (FakeResponse({"error": "private server details"}, 429), "HTTP 429"),
                (FakeResponse({"error": "private server details"}, 503), "HTTP 503"),
                (FakeResponse(ValueError("private server details")), "invalid JSON response"),
                (FakeResponse([{"status": "unexpected shape"}]), "invalid JSON response"),
                (FakeResponse({"error": {"message": "private server details"}}), "request failure"),
            ]:
                with self.subTest(adapter=adapter.__name__, condition=expected_message):
                    client = FakeHttpClient(response)
                    result = ExtractionPipeline(adapter(client)).run(QUOTATION_PDF, "quotation")
                    self.assertEqual(result.status, "failed")
                    self.assertIsNone(result.payload)
                    self.assertEqual(result.errors[0].code, "provider_error")
                    self.assertIn(expected_message, result.errors[0].message)
                    self.assertNotIn("private server details", result.errors[0].message)

    def test_blocked_truncated_and_malformed_responses_fail_instead_of_reaching_review(self):
        refusal = openai_body("{}")
        refusal["output"][1]["content"].append({"type": "refusal", "refusal": "private refusal details"})
        incomplete_message = openai_body("{}")
        incomplete_message["output"][1]["status"] = "incomplete"
        for adapter, body in [
            (GeminiProvider, {"promptFeedback": {"blockReason": "SAFETY"}}),
            (GeminiProvider, gemini_body("{}", finish_reason="MAX_TOKENS")),
            (GeminiProvider, {"candidates": [None]}),
            (GeminiProvider, gemini_body()),
            (GeminiProvider, gemini_body("{}", 123)),
            (OpenAIProvider, openai_body("{}", status="incomplete")),
            (OpenAIProvider, incomplete_message),
            (OpenAIProvider, refusal),
            (OpenAIProvider, {"status": "completed", "output": None}),
            (OpenAIProvider, openai_body()),
            (OpenAIProvider, openai_body("{}", 123)),
        ]:
            with self.subTest(adapter=adapter.__name__, body=body):
                client = FakeHttpClient(FakeResponse(body))
                result = ExtractionPipeline(adapter(client)).run(QUOTATION_PDF, "quotation")
                self.assertEqual(result.status, "failed")
                self.assertEqual(result.errors[0].code, "provider_error")
                self.assertNotIn("private refusal details", result.errors[0].message)

    def test_invalid_kind_or_media_is_rejected_before_http(self):
        for adapter, _ in PROVIDERS:
            with self.subTest(adapter=adapter.__name__):
                client = FakeHttpClient(FakeResponse({}))
                provider = adapter(client)
                with self.assertRaises(ProviderError):
                    provider.extract(Document(b"unvalidated", "application/pdf"), "other")
                with self.assertRaises(ProviderError):
                    provider.extract(Document(b"unvalidated", "text/plain"), "quotation")
                self.assertEqual(client.calls, [])
