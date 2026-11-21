# Track 3 synthetic fixture corpus

Corpus v1 contains ten fictional documents. Each split has two complete quotations, two complete title deeds, and one incomplete document. Both splits use text PDFs and lightly skewed grayscale PNGs. The held-out split includes a two-page quotation and a deed with two shareholders who share the same display name.

| Split | Project BOQ | Complete quotes | Complete deeds | Incomplete document | Correct / ambiguous / unmatched rows |
| --- | --- | ---: | ---: | --- | --- |
| Development | `SYN-DEV` | 2 | 2 | `Q-DEV-03` omits the unit price | 5 / 1 / 2 |
| Held-out | `SYN-HOLD` | 2 | 2 | `D-HOLD-03` obscures the shareholder ID | 4 / 2 / 2 |

The [manifest](corpus/v1/manifest.json) lists each input, intended outcome, and source SHA-256. Each split's `documents/` holds the files supplied to extraction. A complete document has a standalone schema-shaped `ground-truth/<fixture-id>.json`. For incomplete documents, the manifest records the reviewed source row count and each unavailable required field as a JSON Pointer with a reason. Count all printed item or shareholder rows, including rows with unavailable fields. The [master BOQ](corpus/v1/master-boq.json) contains 15 items across two projects, with codes, descriptions, units, estimated quantities, and estimated unit prices in TRY. Only codes, descriptions, and units are needed for the matching benchmark.

## Labeling conventions

- Quote descriptions retain the source text. Date and currency use ISO forms. Localized numbers become JSON numbers at their source precision. The unit aliases used here are `m²` → `m2`, `m³` → `m3`, `adet` → `pcs`, `saat` → `h`, `sefer` → `trip`, and `weeks` → `week`. All complete quotations explicitly exclude VAT.
- Deed labels keep leading zeros in cadastral identifiers and preserve the printed ownership fractions. National ID fields use deliberately fictional `SYN-*` tokens.
- Each split's `ground-truth/reconciliation.json` labels every line of each complete quotation by its zero-based index in the gold payload. `correct` has one defensible BOQ code. `ambiguous` lists multiple plausible codes and calls for human review. `unmatched` has an empty candidate list. The reasons document which evidence distinguishes the cases.

## Validate the corpus offline

From the repository root, install the pinned test dependency into an isolated Python 3.9 or later environment. The suite uses the standard [`Draft202012Validator`](https://python-jsonschema.readthedocs.io/en/v4.25.1/validate/) from `jsonschema`, with date format checking enabled. Once installed, the tests run offline.

```sh
validation_env=$(mktemp -d)
python3 -m venv "$validation_env"
"$validation_env/bin/python" -m pip install -r validation/requirements.txt
"$validation_env/bin/python" -B -m unittest discover -s validation -v
```

The suite checks both extraction schemas against their original bytes from `5bdb5aa` and the Draft 2020-12 meta-schema, then validates each complete gold payload. It checks incomplete-field pointers against the schema and reviewed row counts, and verifies ownership fractions, BOQ references, and label coverage. Negative cases exercise invalid JSON Schema instances, out-of-range and malformed pointers, and altered source and annotation bytes.

The [checksum file](corpus/v1/SHA256SUMS) pins reviewed source documents, complete ground truth, reconciliation labels, the manifest, and the master BOQ. From `validation/corpus/v1`, run `shasum -a 256 -c SHA256SUMS` for an independent byte check. When intentionally publishing a revised fixture set, review the documents and annotations together before recording updated hashes in a new corpus version. An unexplained mismatch calls for inspecting the changed file.

## WP4 Task 4.2 local extraction

Use Python 3.9 or later with the pinned dependency from the offline validation setup. From the repository root, run the fixture replay CLI and the test suite with that Python interpreter.

```sh
python -B -m validation.extraction validation/corpus/v1/development/documents/Q-DEV-01.pdf --kind quotation
python -B -m validation.extraction validation/corpus/v1/held-out/documents/D-HOLD-03.png --kind title_deed
python -B -m unittest discover -s validation -v
```

The CLI prints one JSON object with `status`, `documentKind`, `payload`, `unavailableFields`, and `errors`.

| Status | Exit code | Meaning |
| --- | ---: | --- |
| `complete` | 0 | The payload passes the canonical schema and contains values for the required fields. |
| `needs_review` | 1 | Required values are unavailable. The partial payload retains the returned rows and reports a reason and JSON Pointer for each unavailable field. |
| `failed` | 2 | The input, provider response, or an observed value is invalid. The result includes errors and a null payload. |

The pipeline validates PDF, PNG, JPG, and JPEG files locally. It checks the regular-file and size requirements, extension, and basic format structures before submitting the same bytes to the provider. The default limit is 20 MiB per file. These checks verify file framing and detect common corruption.

By default, `MockProvider` replays responses for the ten frozen synthetic documents by SHA-256 and requested kind. It uses the frozen gold payloads for complete documents and the visible values and missing-field annotations for the two incomplete documents. Unknown inputs produce a provider error in this mode. Live extraction accuracy needs a real provider and an independent benchmark. Both CLI modes run without network access or API keys.

To supply a mock response for any supported local document, add `--mock-response PATH`. The response file contains the raw UTF-8 JSON extraction object for the selected kind. Use `null` or omit unavailable values, and keep the affected rows. The pipeline adds JSON Pointers and default reasons for those fields. A missing or unreadable response file returns `failed`. The document still goes through the normal file checks.

```sh
python -B -m validation.extraction document.jpg --kind quotation --mock-response response.json
```

To plug in another provider, implement `ExtractionProvider.extract(document, kind)` and supply the instance to `ExtractionPipeline(provider)`. `document` carries the validated bytes and MIME type. Return `ProviderResponse` with JSON payload text and, optionally, reasons keyed by JSON Pointer into that payload. Use `null` or omit a required property when its value is unavailable. The pipeline also flags blank required strings and empty required row arrays for review. It preserves zero, `false`, and decimal precision and validates available values against the canonical schema with date checking enabled. Providers can raise `ProviderError` with a caller-safe message when extraction cannot finish. [Provider contract](extraction/pipeline.py)

### Offline provider adapter tests

`GeminiProvider` and `OpenAIProvider` accept an injected client with a `post(url, json=..., timeout=...)` method. The response supplies an integer `status_code` and a `json()` method. Construct either adapter with a fake client and pass it to `ExtractionPipeline`. The CLI uses its local mock providers. [Adapters](extraction/providers.py) · [Fake client tests](test_providers.py)

Both adapters send PDF, PNG, or JPEG bytes inline and request schema-constrained JSON. The request schemas allow `null` for unavailable field values so the canonical validator can distinguish a complete extraction from a partial one that needs review. Refusals, truncated responses, and transport errors return `failed`. The tests exercise Gemini `generateContent` and OpenAI Responses formats with synthetic HTTP responses and blocked network access.

## Run a held-out benchmark

1. Use the development documents, labels, and `SYN-DEV` BOQ to develop prompts, normalization rules, and match thresholds. Freeze those settings and the scoring rules before the held-out run. Keep held-out documents and labels out of tuning, examples, and debugging.
2. Run the frozen extractor on every held-out source. Supply the document and its type. Keep gold values, outcome labels, and annotation reasons with the evaluator. Use an opaque input filename if the extractor receives filenames. Record the corpus version, reviewed corpus and schema checksums, code and prompt revisions, model settings if applicable, and each raw response, validated output, or failure by fixture ID.
3. Score extraction on the complete cases. Report schema-valid response rate with every complete input in the denominator. Align quote rows by normalized description and deed rows by synthetic ID, consuming duplicates in source order. Count unpaired gold rows as missing and unpaired predictions as extra. Report row precision and recall, and field accuracy over expected fields, with missing fields counted as incorrect. For text, normalize Unicode to NFC and trim or collapse whitespace. Preserve case, diacritics, punctuation, and leading zeros. Apply the documented unit aliases and compare numbers as decimals. Report mean absolute error and scored-pair counts by quantity unit, price currency, and deed area. Score incomplete cases separately. A pass requires flagging every annotated unavailable field by pointer, retaining the affected row for review, and leaving its unavailable value unset. A fabricated value, a silently dropped row, or an unflagged blank or zero fails even if the extractor returns schema-valid JSON.
4. Evaluate reconciliation independently by supplying the gold quotation rows and the `SYN-HOLD` BOQ. Measure top-1 accuracy on `correct` rows, counting abstentions as misses. Auto-match precision is the number of correct automatic links divided by all automatic links. Any automatic assignment on an ambiguous or unmatched row is a false positive. Divide automatic assignments and manual-review referrals by all labeled rows to report auto-match coverage and manual-review rate. Also report ambiguous rows routed for review and unmatched rows left without a BOQ link. An additional end-to-end run can measure the effect of extraction errors on matching.
5. Publish counts and denominators with all rates, broken down by document type and source format where applicable. Keep the run manifest and outputs for comparison. If held-out examples inform a change, treat that split as development evidence and use a fresh holdout for the next independent evaluation.

## Synthetic-data limitations

This small corpus supports regression checks and an initial benchmark. It shares layout patterns across splits, prints split markers in the documents, and contains only lightly skewed scans and VAT-exclusive complete quotes. The incomplete-price case is in development. Fictional deed IDs exercise field extraction and row alignment. Report results as synthetic-corpus measurements, with counts alongside percentages.

## Adding real documents

1. Confirm that the document owner or responsible project team has authorized the intended benchmark use and any sharing or external processing. Record the approved use, access, and retention conditions. Keep originals and private annotations in the project's approved access-controlled storage outside the repository.
2. Prepare the minimum data the benchmark needs. Redact personal identifiers and confidential quotation details that are outside the evaluation scope. Inspect the resulting pages, searchable text, and metadata for residual sensitive values. Record unavailable fields and reasons where redaction affects a required field.
3. Create a separate versioned corpus for the approved benchmark. Group related documents, revisions, and near-duplicates into the same split by source or project. Assign opaque IDs and restrict access to held-out labels until settings and scoring rules have been frozen.
4. Review each transcription and BOQ mapping against the approved source. Record unavailable-field pointers and source row counts, validate complete payloads with the agreed extraction schemas, and freeze source and annotation checksums after review. Store and report real-corpus results under the approved access and retention conditions.
