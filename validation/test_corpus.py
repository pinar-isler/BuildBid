"""Offline validation for the frozen Track 3 fixture corpus."""

from collections import Counter
from decimal import Decimal
from fractions import Fraction
from hashlib import sha256
from pathlib import Path
import json
import re
import struct
import unittest

from jsonschema import Draft202012Validator, ValidationError


ROOT = Path(__file__).resolve().parents[1]
CORPUS = ROOT / "validation" / "corpus" / "v1"
SCHEMAS = {
    "quotation": (
        "Vendor_Quotation_Schema",
        "056f0545f3c52ec130295b4c9a3bcf5a985ca73deaaf6c949d2e3af99bc554c9",
    ),
    "title_deed": (
        "Title_deed_Schema",
        "74bc43a743da44961ef48e8e4341799599cadc1e18c49c69738f8b980cb44345",
    ),
}
LABELS = {"correct", "ambiguous", "unmatched"}


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON property {key!r}")
        result[key] = value
    return result


def reject_constant(token):
    raise ValueError(f"{token} is not a JSON number")


def load_json(path):
    with path.open(encoding="utf-8") as source:
        return json.load(
            source,
            parse_float=Decimal,
            parse_constant=reject_constant,
            object_pairs_hook=unique_object,
        )


def read_reviewed_hashes(path):
    hashes = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  (.+)", line)
        if not match:
            raise ValueError(f"Invalid checksum entry: {line!r}")
        digest, name = match.groups()
        if name in hashes:
            raise ValueError(f"Duplicate checksum entry: {name}")
        hashes[name] = digest
    return hashes


def verify_sha256(data, expected, name):
    actual = sha256(data).hexdigest()
    if actual != expected:
        raise ValueError(f"SHA-256 mismatch for {name}: expected {expected}, got {actual}")


def required_field_pointer(schema, pointer, row_count):
    """Check a pointer to a required scalar and bound any index to the source rows."""
    if not isinstance(pointer, str) or not pointer.startswith("/"):
        return False
    for token in pointer[1:].split("/"):
        if schema["type"] == "array" and re.fullmatch(r"0|[1-9][0-9]*", token):
            if int(token) >= row_count:
                return False
            schema = schema["items"]
        elif schema["type"] == "object" and token in schema["required"]:
            schema = schema["properties"][token]
        else:
            return False
    return schema["type"] in {"string", "number", "boolean"}


def validate_incomplete_annotation(entry, schema):
    row_count = entry.get("sourceRowCount")
    if type(row_count) is not int or row_count < 1:
        raise ValueError("sourceRowCount must be a positive integer")
    fields = entry.get("unavailableFields")
    if not isinstance(fields, list) or not fields:
        raise ValueError("unavailableFields must be a non-empty list")
    pointers = set()
    for field in fields:
        if not isinstance(field, dict) or field.keys() != {"pointer", "reason"}:
            raise ValueError("Each unavailable field must have a pointer and a reason")
        pointer = field["pointer"]
        if not required_field_pointer(schema, pointer, row_count):
            raise ValueError(f"Pointer must identify a required field within the source: {pointer!r}")
        if pointer in pointers:
            raise ValueError(f"Duplicate unavailable pointer: {pointer}")
        if not isinstance(field["reason"], str) or not field["reason"].strip():
            raise ValueError(f"Missing reason for {pointer}")
        pointers.add(pointer)


class CorpusTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.manifest = load_json(CORPUS / "manifest.json")
        cls.splits = cls.manifest["splits"]
        cls.schemas = {kind: load_json(ROOT / "Schemas" / name)
                       for kind, (name, _) in SCHEMAS.items()}
        cls.validators = {}
        for kind, schema in cls.schemas.items():
            Draft202012Validator.check_schema(schema)
            cls.validators[kind] = Draft202012Validator(
                schema, format_checker=Draft202012Validator.FORMAT_CHECKER
            )
        cls.boq = load_json(CORPUS / "master-boq.json")
        cls.reviewed_hashes = read_reviewed_hashes(CORPUS / "SHA256SUMS")

    def test_original_schema_bytes(self):
        for name, digest in SCHEMAS.values():
            with self.subTest(schema=name):
                verify_sha256((ROOT / "Schemas" / name).read_bytes(), digest, name)

    def test_reviewed_artifact_integrity(self):
        expected = {"manifest.json", "master-boq.json"}
        for split in self.splits:
            for folder in ("documents", "ground-truth"):
                expected.update(
                    path.relative_to(CORPUS).as_posix()
                    for path in (CORPUS / split / folder).iterdir()
                )
        self.assertCountEqual(self.reviewed_hashes, expected)
        for name in sorted(expected):
            with self.subTest(artifact=name):
                verify_sha256((CORPUS / name).read_bytes(), self.reviewed_hashes[name], name)

    def test_inventory_and_media_integrity(self):
        self.assertEqual(self.manifest["version"], 1)
        self.assertEqual(set(self.splits), {"development", "held-out"})
        hashes = set()
        ids = set()
        for split, details in self.splits.items():
            documents = CORPUS / split / "documents"
            self.assertCountEqual(
                (path.name for path in documents.iterdir()),
                (entry["file"] for entry in details["documents"]),
            )
            complete_formats = set()
            for entry in details["documents"]:
                with self.subTest(split=split, document=entry["id"]):
                    fixture_id = entry["id"]
                    self.assertNotIn(fixture_id, ids)
                    ids.add(fixture_id)
                    self.assertIn(entry["kind"], SCHEMAS)
                    self.assertIn(entry["outcome"], {"complete", "incomplete"})
                    media = documents / entry["file"]
                    self.assertEqual(entry["file"], fixture_id + media.suffix)
                    data = media.read_bytes()
                    digest = sha256(data).hexdigest()
                    self.assertEqual(digest, entry["sha256"])
                    self.assertNotIn(digest, hashes, "duplicate document across corpus")
                    hashes.add(digest)
                    if media.suffix == ".pdf":
                        self.assertTrue(data.startswith(b"%PDF-"))
                        self.assertTrue(data.rstrip().endswith(b"%%EOF"))
                    else:
                        self.assertEqual(media.suffix, ".png")
                        self.assertEqual(data[:8], b"\x89PNG\r\n\x1a\n")
                        self.assertEqual(data[12:16], b"IHDR")
                        width, height = struct.unpack(">II", data[16:24])
                        self.assertGreaterEqual(width, 1000)
                        self.assertGreaterEqual(height, 1000)
                    if entry["outcome"] == "complete":
                        complete_formats.add((entry["kind"], media.suffix))
            self.assertEqual(complete_formats, {(kind, suffix) for kind in SCHEMAS
                                                for suffix in (".pdf", ".png")})

    def test_ground_truth_and_incomplete_expectations(self):
        shareholder_ids = set()
        incomplete_kinds = set()
        for split, details in self.splits.items():
            ground_truth = CORPUS / split / "ground-truth"
            expected_files = ["reconciliation.json"]
            for entry in details["documents"]:
                fixture_id = entry["id"]
                kind = entry["kind"]
                with self.subTest(split=split, document=fixture_id):
                    if entry["outcome"] == "incomplete":
                        incomplete_kinds.add(kind)
                        validate_incomplete_annotation(entry, self.schemas[kind])
                        continue
                    self.assertNotIn("unavailableFields", entry)
                    self.assertNotIn("sourceRowCount", entry)
                    name = fixture_id + ".json"
                    expected_files.append(name)
                    expected = load_json(ground_truth / name)
                    self.validators[kind].validate(expected)
                    if kind == "quotation":
                        self.assertTrue(expected["vendorName"].strip())
                        self.assertGreater(len(expected["extractedItems"]), 0)
                        for row in expected["extractedItems"]:
                            self.assertTrue(row["rawDescription"].strip())
                            self.assertTrue(row["unitOfMeasure"].strip())
                    else:
                        self.assertGreater(expected["totalAreaM2"], 0)
                        self.assertRegex(expected["blockAda"], r"^[0-9]+$")
                        self.assertRegex(expected["parcelParsel"], r"^[0-9]+$")
                        fractions = []
                        for shareholder in expected["shareholders"]:
                            identifier = shareholder["nationalId"]
                            self.assertRegex(identifier, r"^SYN-(DEV|HOLD)-[0-9]{3}$")
                            self.assertNotIn(identifier, shareholder_ids)
                            shareholder_ids.add(identifier)
                            self.assertTrue(shareholder["fullName"].strip())
                            share = shareholder["shareFraction"]
                            self.assertRegex(share, r"^[1-9][0-9]*/[1-9][0-9]*$")
                            fractions.append(Fraction(share))
                        self.assertEqual(sum(fractions), 1)
            self.assertCountEqual((path.name for path in ground_truth.iterdir()), expected_files)
        self.assertEqual(incomplete_kinds, set(SCHEMAS))

    def test_boq_and_reconciliation(self):
        self.assertCountEqual(
            self.boq.keys(), (details["boqProjectId"] for details in self.splits.values())
        )
        all_codes = set()
        for split, details in self.splits.items():
            boq_units = {}
            for item in self.boq[details["boqProjectId"]]:
                code = item["itemCode"]
                self.assertTrue(code.strip())
                self.assertNotIn(code, all_codes)
                self.assertTrue(item["description"].strip())
                self.assertTrue(item["unitOfMeasure"].strip())
                for key in ("estimatedQuantity", "estimatedUnitPrice"):
                    self.assertIn(type(item[key]), (int, Decimal))
                    self.assertGreater(item[key], 0)
                all_codes.add(code)
                boq_units[code] = item["unitOfMeasure"]
            ground_truth = CORPUS / split / "ground-truth"
            annotation = load_json(ground_truth / "reconciliation.json")
            complete_quotations = [
                entry["id"] for entry in details["documents"]
                if entry["kind"] == "quotation" and entry["outcome"] == "complete"
            ]
            self.assertCountEqual(annotation.keys(), complete_quotations)
            label_counts = Counter()
            for fixture_id in complete_quotations:
                items = load_json(ground_truth / (fixture_id + ".json"))["extractedItems"]
                labels = annotation[fixture_id]
                with self.subTest(split=split, document=fixture_id):
                    self.assertCountEqual([row["itemIndex"] for row in labels], range(len(items)))
                    for row in labels:
                        self.assertIs(type(row["itemIndex"]), int)
                        label = row["label"]
                        self.assertIn(label, LABELS)
                        label_counts[label] += 1
                        candidates = row["candidateItemCodes"]
                        self.assertIsInstance(candidates, list)
                        self.assertEqual(len(candidates), len(set(candidates)))
                        self.assertTrue(row["reason"].strip())
                        if label == "ambiguous":
                            self.assertGreaterEqual(len(candidates), 2)
                        else:
                            self.assertEqual(len(candidates), 1 if label == "correct" else 0)
                        for code in candidates:
                            self.assertIn(code, boq_units, "candidate must belong to this project")
                            self.assertEqual(boq_units[code], items[row["itemIndex"]]["unitOfMeasure"])
            self.assertEqual(set(label_counts), LABELS)

    def test_validator_rejects_invalid_quotation_payloads(self):
        path = CORPUS / "development" / "ground-truth" / "Q-DEV-01.json"
        validator = self.validators["quotation"]
        for key, value, keyword in [
            ("quotationDate", "2026-02-30", "format"),
            ("quotationDate", "20260312", "format"),
            ("currency", "GBP", "enum"),
            ("extra", True, "additionalProperties"),
        ]:
            with self.subTest(field=key, value=value):
                output = load_json(path)
                output[key] = value
                with self.assertRaises(ValidationError) as error:
                    validator.validate(output)
                self.assertEqual(error.exception.validator, keyword)
        for key, value, keyword in [
            ("quantity", True, "type"),
            ("quotedUnitPrice", -1, "minimum"),
            ("isTaxIncluded", "false", "type"),
            ("extra", 0, "additionalProperties"),
        ]:
            with self.subTest(field=key):
                output = load_json(path)
                output["extractedItems"][0][key] = value
                with self.assertRaises(ValidationError) as error:
                    validator.validate(output)
                self.assertEqual(error.exception.validator, keyword)

    def test_validator_rejects_invalid_title_deed_payloads(self):
        path = CORPUS / "development" / "ground-truth" / "D-DEV-01.json"
        for key, value, keyword in [
            ("parcelParsel", 7, "type"),
            ("totalAreaM2", -1, "minimum"),
            ("shareholders", {}, "type"),
        ]:
            with self.subTest(field=key):
                output = load_json(path)
                output[key] = value
                with self.assertRaises(ValidationError) as error:
                    self.validators["title_deed"].validate(output)
                self.assertEqual(error.exception.validator, keyword)

    def test_validator_rejects_missing_annotated_fields(self):
        examples = {
            "quotation": ("Q-DEV-01.json", "extractedItems", "quotedUnitPrice"),
            "title_deed": ("D-DEV-01.json", "shareholders", "nationalId"),
        }
        for kind, (name, rows, field) in examples.items():
            with self.subTest(kind=kind):
                output = load_json(CORPUS / "development" / "ground-truth" / name)
                del output[rows][0][field]
                with self.assertRaises(ValidationError) as error:
                    self.validators[kind].validate(output)
                self.assertEqual(error.exception.validator, "required")
                self.assertEqual(list(error.exception.absolute_path), [rows, 0])

    def test_incomplete_annotation_rejects_invalid_pointers_and_reasons(self):
        for kind, rows, field in [
            ("quotation", "extractedItems", "quotedUnitPrice"),
            ("title_deed", "shareholders", "nationalId"),
        ]:
            for pointer, reason in [
                (f"/{rows}/999/{field}", "source row does not exist"),
                (f"/{rows}/01/{field}", "index has a leading zero"),
                (f"/{rows}/0/unknown", "field does not exist"),
                (f"/{rows}/0", "pointer does not end at a scalar"),
                (f"/{rows}/0/{field}", " "),
                (f"/{rows}/0/{field}", None),
            ]:
                with self.subTest(kind=kind, pointer=pointer, reason=reason):
                    entry = {
                        "sourceRowCount": 1,
                        "unavailableFields": [{"pointer": pointer, "reason": reason}],
                    }
                    with self.assertRaises(ValueError):
                        validate_incomplete_annotation(entry, self.schemas[kind])

    def test_incomplete_annotation_rejects_missing_or_duplicate_entries(self):
        entry = next(
            doc for doc in self.splits["development"]["documents"]
            if doc["outcome"] == "incomplete"
        )
        schema = self.schemas["quotation"]
        for key, value, message in [
            ("sourceRowCount", True, "positive integer"),
            ("sourceRowCount", 0, "positive integer"),
            ("unavailableFields", [], "non-empty list"),
            ("unavailableFields", [{"pointer": "/extractedItems/0/quotedUnitPrice"}],
             "pointer and a reason"),
            ("unavailableFields", entry["unavailableFields"] * 2, "Duplicate unavailable pointer"),
        ]:
            with self.subTest(field=key, value=value):
                variant = {**entry, key: value}
                with self.assertRaisesRegex(ValueError, message):
                    validate_incomplete_annotation(variant, schema)

    def test_integrity_rejects_modified_reviewed_artifacts(self):
        mutations = [
            ("development/documents/Q-DEV-01.pdf", None, b"\n"),
            ("development/ground-truth/Q-DEV-01.json", b"2450.75", b"24500.75"),
            ("development/ground-truth/reconciliation.json", b"DEV-REBAR-12", b"DEV-REBAR-16"),
            ("manifest.json", b"/extractedItems/0/quotedUnitPrice",
             b"/extractedItems/999/quotedUnitPrice"),
        ]
        for name, original, replacement in mutations:
            with self.subTest(artifact=name):
                data = (CORPUS / name).read_bytes()
                changed = data.replace(original, replacement, 1) if original else data + replacement
                self.assertNotEqual(data, changed)
                with self.assertRaisesRegex(ValueError, "SHA-256 mismatch"):
                    verify_sha256(changed, self.reviewed_hashes[name], name)
