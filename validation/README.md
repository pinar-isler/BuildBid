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

WP4 uses a standalone module under `validation/` and requires Go 1.25 or later. The module path is provisional pending agreement on backend integration. Run these commands from the repository root. Once the pinned Go dependencies are available in the module cache, the tests and corpus benchmarks run offline. [Module definition](go.mod)

```sh
go -C validation test -count=1 ./...
go -C validation vet ./...
go -C validation mod verify
go -C validation mod tidy -diff
go -C validation build ./cmd/...
```

The suite checks both extraction schemas against their original bytes from `5bdb5aa` and the Draft 2020-12 meta-schema, then validates each complete gold payload. It checks incomplete-field pointers against the schema and reviewed row counts, and verifies ownership fractions, BOQ references, and label coverage. Negative cases exercise invalid JSON Schema instances, out-of-range and malformed pointers, and altered source and annotation bytes.

The [checksum file](corpus/v1/SHA256SUMS) pins reviewed source documents, complete ground truth, reconciliation labels, the manifest, and the master BOQ. From `validation/corpus/v1`, run `shasum -a 256 -c SHA256SUMS` for an independent byte check. When intentionally publishing a revised fixture set, review the documents and annotations together before recording updated hashes in a new corpus version. An unexplained mismatch calls for inspecting the changed file.

## WP4 Task 4.2 local extraction

Build the CLI from the repository root to use its exit codes directly. The examples cover a complete fixture, an incomplete fixture, and a saved response for a local document.

```sh
go -C validation build -o ./extract ./cmd/extract
./validation/extract validation/corpus/v1/development/documents/Q-DEV-01.pdf --kind quotation
./validation/extract validation/corpus/v1/held-out/documents/D-HOLD-03.png --kind title_deed
./validation/extract document.jpg --kind quotation --mock-response response.json
```

The CLI locates the checkout from the working directory. Supply `--repo-root PATH` when running it elsewhere.

The CLI prints one JSON object with `status`, `documentKind`, `payload`, `unavailableFields`, and `errors`.

| Status | Exit code | Meaning |
| --- | ---: | --- |
| `complete` | 0 | The payload passes the canonical schema and contains values for the required fields. |
| `needs_review` | 1 | Required values are unavailable. The partial payload retains the returned rows and reports a reason and JSON Pointer for each unavailable field. |
| `failed` | 2 | The input, provider response, or an observed value is invalid. The result includes errors and a null payload. |

The pipeline validates PDF, PNG, JPG, and JPEG files locally. It checks the regular-file and size requirements, extension, and basic format structures before submitting the same bytes to the provider. The default limit is 20 MiB per file. These checks verify file framing and detect common corruption.

By default, `MockProvider` replays responses for the ten frozen synthetic documents by SHA-256 and requested kind. It uses the frozen gold payloads for complete documents and the visible values and missing-field annotations for the two incomplete documents. Unknown inputs produce a provider error in this mode. Live extraction accuracy needs a real provider and an independent benchmark. Both CLI modes run without network access or API keys.

To supply a mock response for any supported local document, add `--mock-response PATH`. The response file contains the raw UTF-8 JSON extraction object for the selected kind. Use `null` or omit unavailable values, and keep the affected rows. The pipeline adds JSON Pointers and default reasons for those fields. A missing or unreadable response file returns `failed`. The document still goes through the normal file checks.

The pipeline compiles the canonical Draft 2020-12 schemas from `Schemas/`, enables date assertions, and disables external schema loading. `Validators.ValidateCanonical` checks the original strict JSON value. The pipeline separately checks a review projection and flags omitted or null required values, blank required strings, and empty required row arrays while retaining the original rows. It preserves zero, `false`, and JSON number spellings. [Go extraction package](extraction/), [strict JSON package](strictjson/)

### Go provider adapters

`NewGeminiProvider` and `NewOpenAIProvider` accept a caller-supplied `http.RoundTripper`, a filesystem rooted at `Schemas/`, and `Options`. Pass the resulting `extraction.Provider` to `extraction.NewPipeline`. The provider receives validated document bytes, MIME type, and SHA-256. It returns a `ProviderResponse` with JSON text and optional unavailable-field reasons keyed by JSON Pointer. The defaults are `gemini-3.5-flash-lite`, `gpt-6-luna`, and a 30-second request timeout. The extraction CLI uses its local mocks. [Go providers](extraction/providers/)

By default the adapters leave credentials to the injected transport. To opt in to environment lookup, supply `Options.LookupEnv` (for example, `os.LookupEnv`). Gemini checks `GOOGLE_API_KEY` then `GEMINI_API_KEY` and uses the `x-goog-api-key` header. OpenAI checks `OPENAI_API_KEY` and uses a bearer header. The adapters stop on redirects. The tests inject fake transports and synthetic lookup values. [Google API key documentation](https://aistudio.google.com/docs/api-key)

The adapters send PDF, PNG, or JPEG bytes inline. Their request schemas retain canonical property order and constraints while allowing unavailable property values to be null. The extraction pipeline checks returned model text locally. Refusals, truncated responses, and transport errors return `failed`. Fake HTTP tests cover Gemini `generateContent` and OpenAI Responses without API calls. Live acceptance of the exact request schemas is unverified. Providers document supported subsets of JSON Schema. [Gemini structured output](https://aistudio.google.com/docs/structured-output), [OpenAI structured outputs](https://developers.openai.com/api/docs/guides/structured-outputs)

```sh
go -C validation test ./extraction/providers
```

## WP4 Task 4.3 BOQ matching

Use `matching.NewBOQMatcher` with the master BOQ rows for the quotation's project. `MatchQuotation` returns one result per extracted row in source order, and `MatchItem` scores a single row. Each result includes a status, a ranked candidate list with scores and unconfirmed specifications, and a reason. Only `matched` results carry a selected BOQ code. `ambiguous` results go to manual review. [Matching engine](matching/)

The matcher folds Turkish case and diacritics, resolves common unit aliases, and aligns a small set of English and Turkish construction terms. Quotation units must agree after alias normalization. The matcher compares parsed concrete and steel grades, fire ratings, application locations, nominal and physical diameters, and dimensions separately. Conflicting specifications exclude a candidate. A plausible candidate with an unconfirmed specification, such as door width or a full concrete grade, goes to review. Measurements in mm, cm, and m are normalized within descriptions. Compound dimensions keep their printed order.

The text score is the higher of two normalized Levenshtein scores, one in source word order and one in alphabetical word order. The matcher keeps candidates at or above `0.60`. An automatic link requires a score of at least `0.68`, confirmed specifications, and a lead of more than `0.08` over the next candidate. Equal scores are displayed in BOQ-code order for reproducibility.

### Benchmark results and evaluation status

- **Development results.** `SYN-DEV` contains the eight rows used to develop the matching rules.
- **Initial held-out results.** The first run on `SYN-HOLD` is the historical baseline before the bilingual term and dimension-handling adjustments. It linked 2 of 4 linkable rows and sent 0 of 2 ambiguous rows to review.
- **Post-adjustment regression results.** The misses from the initial run were used to debug the matcher. `SYN-HOLD` is now a regression set for Task 4.3. The revised matcher linked 4 of 4 linkable rows and sent 2 of 2 ambiguous rows to review on those same eight rows.

| Metric | Development | Initial held-out baseline | Post-adjustment regression on `SYN-HOLD` |
| --- | ---: | ---: | ---: |
| Top-1 accuracy on linkable rows | 5/5 | 2/4 | 4/4 |
| Automatic match precision | 5/5 | 2/2 | 4/4 |
| Automatic match coverage | 5/8 | 2/8 | 4/8 |
| Manual review rate | 1/8 | 0/8 | 2/8 |
| Ambiguous rows sent to review | 1/1 | 0/2 | 2/2 |
| Unmatched rows left unlinked | 2/2 | 2/2 | 2/2 |
| Correct decisions overall | 8/8 | 4/8 | 8/8 |

**The 90% RAD target remains unverified.** Assess it on a fresh, separately labeled evaluation set whose documents, BOQ rows, and labels have been kept out of tuning and debugging. Before running the evaluation, freeze the matcher, thresholds, and scoring rules, and confirm the RAD metric and its denominator. Report the measured result and sample counts from that fresh set.

The offline evaluator runs the current matcher against gold quotation rows and the project BOQ. Its JSON reports label the phases `development` and `post_adjustment_regression`. Both reports have `independentEvaluation: false`. The initial held-out counts are recorded in the table above. [Go matching benchmark](benchmark/matching/)

```sh
go -C validation run ./cmd/evaluate-matching --split development
go -C validation run ./cmd/evaluate-matching --split held-out
go -C validation run ./cmd/evaluate-matching --split both
```

The built command discovers corpus v1 from the repository root or `validation/`. Use `--corpus PATH` to supply a specific corpus directory. The default split is `both`. The binary exits with code `2` for invalid arguments and `1` for corpus or report failures.

## WP4 Task 4.4 extraction evaluation

Run the evaluator with fixture replay for an operational smoke test, or supply a saved bundle of raw predictions for quality scoring. The evaluator runs locally, with one source per report. Mock timing measures the local pipeline replay. [Go extraction benchmark](benchmark/extraction/)

Save the original provider response text in `payloadJson`, including malformed JSON when that is what the provider produced. Use `null` for a recorded attempt that produced no text. The prediction bundle declares `source` as `live` for captured live results or `mock` for synthetic results. The report relies on this declaration. Keep provider and model runs in separate bundles. If a record repeats `source`, `provider`, or `model`, its value must agree with the bundle. Each record may supply `latencyMs`, integer `inputTokens`, `outputTokens`, and `totalTokens`, `costUsd`, and an `unavailableReasons` object keyed by JSON Pointer. Token and cost totals cover only records with supplied values, including failed attempts. Latency percentiles use nearest rank over measured documents. Zero counts as a recorded value. Unknown telemetry remains unavailable.

```json
{
  "source": "live",
  "provider": "provider-name",
  "model": "model-id",
  "predictions": {
    "Q-DEV-01": {"payloadJson": null, "latencyMs": 815.2, "inputTokens": 620}
  }
}
```

The example shows the bundle shape for one recorded attempt with no response. Missing fixture records count as failures for the selected split. Reports distinguish missing predictions, attempts with no response, invalid JSON, and schema errors. JSON reports include per-document outcomes, missing field pointers, telemetry coverage, and summaries by split, document kind, and source format. They include corpus manifest and schema hashes for reproducibility. The console format summarizes the same run's metrics.

### Go extraction benchmark

Run these commands from the repository root. Use an absolute path for a saved bundle because `go -C validation` sets the command's working directory. The `--split` flag accepts `development`, `held-out`, or `both` and defaults to `both`. The `--format` flag accepts `json` or `console` and defaults to `console`.

```sh
go -C validation run ./cmd/evaluate-extraction --mock --split both
go -C validation run ./cmd/evaluate-extraction --mock --format json
go -C validation run ./cmd/evaluate-extraction --predictions /absolute/path/to/predictions.json --format json
go -C validation test ./benchmark/extraction ./cmd/evaluate-extraction
```

Mock replay and saved mock bundles exercise the pipeline and operational reporting with `quality: null`. Saved predictions declared `live` by the caller produce quality metrics, with `independentEvaluation: false`. **The 85% extraction target remains independently unverified.** Independent accuracy requires fresh evaluation data and verified captures.

Numeric summaries use 50 significant digits, half-even rounding, and the reference 64-bit decimal exponent bounds. Token totals are exact integers. The JSON and console reports publish absolute-error summaries. The Go package also exposes `RelativeError`, which is unavailable when the reference value is zero, and `FieldCounts.Extra` for present fields on unaligned rows. [Go extraction benchmark](benchmark/extraction/)

On complete cases, field accuracy and recall use the number of expected scalar values as the denominator. Field precision uses the number of predicted scalar values, including those on extra rows. Wrong values count as both a missed expected value and an incorrect predicted value for F1. Row precision, recall, and F1 use the number of aligned quotation or shareholder rows. Alignment uses normalized descriptions for quotations and IDs for deeds, consuming duplicates in source order. Rates with empty denominators are unavailable. Schema validity checks the original parsed response against the canonical schema. A prediction rejected by the extraction pipeline contributes missing expected fields and rows to the quality scores. Numeric calculations use 50 significant decimal digits and report mean and maximum absolute errors with scored and expected pair counts. Quantities require matching units, and unit prices require matching units and currency. Incomplete cases are scored separately for retaining the expected number of rows, flagging the expected unavailable fields, and leaving those values unset.

## Compatibility limits

The verified Go toolchain and `golang.org/x/text` use Unicode 15.0.0. The former Python 3.9 reference used Unicode 13.0.0, so characters added in later Unicode versions can normalize differently. BOQ measurement conversion uses 28 significant decimal digits with half-even rounding.

Standalone extraction preserves JSON number spellings and accepts finite exponents beyond the reference decimal range, such as `1e1000000000000000000`. The recorded evaluator applies the reference bounds and reports that example as `invalid_json`. The migration checks covered corpus v1 and targeted regression inputs.

## Run a held-out benchmark

1. Use the development documents, labels, and `SYN-DEV` BOQ to develop prompts, normalization rules, and match thresholds. Freeze those settings and the scoring rules before the held-out run. Keep held-out documents and labels out of tuning, examples, and debugging.
2. Run the frozen extractor on every held-out source. Supply the document and its type. Keep gold values, outcome labels, and annotation reasons with the evaluator. Use an opaque input filename if the extractor receives filenames. Record the corpus version, reviewed corpus and schema checksums, code and prompt revisions, model settings if applicable, and each raw response, validated output, or failure by fixture ID.
3. Score extraction on the complete cases. Report schema-valid response rate with every complete input in the denominator. Align quote rows by normalized description and deed rows by synthetic ID, consuming duplicates in source order. Count unpaired gold rows as missing and unpaired predictions as extra. Report row precision and recall, and field accuracy over expected fields, with missing fields counted as incorrect. For text, normalize Unicode to NFC and trim or collapse whitespace. Preserve case, diacritics, punctuation, and leading zeros. Apply the documented unit aliases and compare numbers as decimals. Report mean absolute error and scored-pair counts by quantity unit, price currency, and deed area. Score incomplete cases separately. A pass requires flagging every annotated unavailable field by pointer, retaining the affected row for review, and leaving its unavailable value unset. A fabricated value, a silently dropped row, or an unflagged blank or zero fails even if the extractor returns schema-valid JSON.
4. To isolate reconciliation from extraction, supply the gold quotation rows and their project BOQ. For Task 4.3, `SYN-HOLD` supplies regression results and a fresh evaluation set is required for independent scoring. Measure top-1 accuracy on `correct` rows, counting abstentions as misses. Auto-match precision is the number of correct automatic links divided by all automatic links. Any automatic assignment on an ambiguous or unmatched row is a false positive. Divide automatic assignments and manual-review referrals by all labeled rows to report auto-match coverage and manual-review rate. Also report ambiguous rows routed for review and unmatched rows left without a BOQ link. An additional end-to-end run can measure the effect of extraction errors on matching.
5. Publish counts and denominators with all rates, broken down by document type and source format where applicable. Keep the run manifest and outputs for comparison. If held-out examples inform a change, treat that split as development evidence and use a fresh holdout for the next independent evaluation.

## Synthetic-data limitations

This small corpus supports regression checks and an initial benchmark. It shares layout patterns across splits, prints split markers in the documents, and contains only lightly skewed scans and VAT-exclusive complete quotes. The incomplete-price case is in development. Fictional deed IDs exercise field extraction and row alignment. Report results as synthetic-corpus measurements, with counts alongside percentages.

## Adding real documents

1. Confirm that the document owner or responsible project team has authorized the intended benchmark use and any sharing or external processing. Record the approved use, access, and retention conditions. Keep originals and private annotations in the project's approved access-controlled storage outside the repository.
2. Prepare the minimum data the benchmark needs. Redact personal identifiers and confidential quotation details that are outside the evaluation scope. Inspect the resulting pages, searchable text, and metadata for residual sensitive values. Record unavailable fields and reasons where redaction affects a required field.
3. Create a separate versioned corpus for the approved benchmark. Group related documents, revisions, and near-duplicates into the same split by source or project. Assign opaque IDs and restrict access to held-out labels until settings and scoring rules have been frozen.
4. Review each transcription and BOQ mapping against the approved source. Record unavailable-field pointers and source row counts, validate complete payloads with the agreed extraction schemas, and freeze source and annotation checksums after review. Store and report real-corpus results under the approved access and retention conditions.
