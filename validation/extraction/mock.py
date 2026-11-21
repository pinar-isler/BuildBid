"""Local mock providers for corpus replay and explicit JSON responses."""

from hashlib import sha256
import json
from pathlib import Path

from .media import Document
from .pipeline import ROOT, ProviderError, ProviderResponse


CORPUS = ROOT / "validation" / "corpus" / "v1"

# Transcribe the visible values in the two incomplete source documents.
PARTIAL_PAYLOADS = {
    "Q-DEV-03": {
        "vendorName": "Deneme Temel İşleri",
        "quotationDate": "2026-05-19",
        "currency": "TRY",
        "extractedItems": [{
            "rawDescription": "Temel kazısı ve nakliye",
            "quantity": 31.25,
            "unitOfMeasure": "m3",
            "quotedUnitPrice": None,
            "isTaxIncluded": False,
        }],
    },
    "D-HOLD-03": {
        "city": "Bursa",
        "district": "Nilüfer",
        "blockAda": "0084",
        "parcelParsel": "0030",
        "totalAreaM2": 2640.75,
        "shareholders": [{
            "nationalId": None,
            "fullName": "Kurgu Paydaş K",
            "shareFraction": "1/1",
        }],
    },
}


class MockProvider:
    def __init__(self):
        manifest = json.loads((CORPUS / "manifest.json").read_text(encoding="utf-8"))
        self._fixtures = {
            (entry["kind"], entry["sha256"]): (split, entry)
            for split, group in manifest["splits"].items()
            for entry in group["documents"]
        }

    def extract(self, document: Document, kind: str) -> ProviderResponse:
        fixture = self._fixtures.get((kind, sha256(document.content).hexdigest()))
        if fixture is None:
            raise ProviderError("The mock has no fixture for this document and kind.")

        split, entry = fixture
        fixture_id = entry["id"]
        if entry["outcome"] == "incomplete":
            reasons = {field["pointer"]: field["reason"] for field in entry["unavailableFields"]}
            return ProviderResponse(json.dumps(PARTIAL_PAYLOADS[fixture_id]), reasons)
        path = CORPUS / split / "ground-truth" / f"{fixture_id}.json"
        return ProviderResponse(path.read_text(encoding="utf-8"))


class MockResponseProvider:
    def __init__(self, response_path: Path):
        self._response_path = response_path

    def extract(self, document: Document, kind: str) -> ProviderResponse:
        message = "The mock response must be a readable UTF-8 file."
        try:
            if not self._response_path.is_file():
                raise ProviderError(message)
            return ProviderResponse(self._response_path.read_text(encoding="utf-8"))
        except (OSError, UnicodeError) as error:
            raise ProviderError(message) from error
