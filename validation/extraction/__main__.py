"""Run the local mock extraction CLI."""

import argparse
from dataclasses import asdict
from pathlib import Path

from .jsonio import dumps_json
from .mock import MockProvider, MockResponseProvider
from .pipeline import SCHEMA_FILES, ExtractionPipeline


EXIT_CODES = {"complete": 0, "needs_review": 1, "failed": 2}


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate a local mock BuildBid extraction.")
    parser.add_argument("input", type=Path, help="Path to a PDF, PNG, JPG, or JPEG document")
    parser.add_argument("--kind", required=True, choices=SCHEMA_FILES, help="Document kind")
    parser.add_argument("--mock-response", type=Path, metavar="PATH",
                        help="Use a local UTF-8 JSON payload in place of fixture replay")
    args = parser.parse_args()

    provider = MockResponseProvider(args.mock_response) if args.mock_response else MockProvider()
    result = ExtractionPipeline(provider).run(args.input, args.kind)
    print(dumps_json({
        "status": result.status,
        "documentKind": result.kind,
        "payload": result.payload,
        "unavailableFields": [asdict(field) for field in result.unavailable_fields],
        "errors": [asdict(error) for error in result.errors],
    }))
    return EXIT_CODES[result.status]


if __name__ == "__main__":
    raise SystemExit(main())
