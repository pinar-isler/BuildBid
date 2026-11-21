"""Bounded local reads and structural checks for supported document formats."""

from dataclasses import dataclass
from pathlib import Path
import re
import struct
import zlib


DEFAULT_MAX_BYTES = 20 * 1024 * 1024
MEDIA_TYPES = {
    ".pdf": "application/pdf",
    ".png": "image/png",
    ".jpg": "image/jpeg",
    ".jpeg": "image/jpeg",
}
PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"
JPEG_FRAME_MARKERS = {0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7,
                      0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF}


class InputError(ValueError):
    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code


@dataclass(frozen=True)
class Document:
    content: bytes
    media_type: str


def _valid_pdf(content: bytes) -> bool:
    if not re.match(rb"%PDF-(?:1\.[0-7]|2\.0)[\r\n]", content):
        return False
    trailer = re.search(rb"startxref\s+(\d{1,10})\s+%%EOF[\t\n\f\r \x00]*\Z",
                        content[-4096:])
    if not trailer:
        return False
    offset = int(trailer[1])
    trailer_offset = max(0, len(content) - 4096) + trailer.start()
    return offset < trailer_offset and bool(
        re.match(rb"(?:xref[\t\r\n ]|\d+\s+\d+\s+obj\b)", content[offset:offset + 64])
    )


def _valid_png(content: bytes) -> bool:
    if not content.startswith(PNG_SIGNATURE):
        return False

    offset = len(PNG_SIGNATURE)
    has_image_data = False
    while offset + 12 <= len(content):
        length, chunk_type = struct.unpack_from(">I4s", content, offset)
        end = offset + 8 + length
        if end + 4 > len(content):
            return False
        checksum, = struct.unpack_from(">I", content, end)
        if zlib.crc32(memoryview(content)[offset + 4:end]) != checksum:
            return False

        if offset == len(PNG_SIGNATURE):
            if chunk_type != b"IHDR" or length != 13:
                return False
            width, height = struct.unpack_from(">II", content, offset + 8)
            if not (0 < width < 2**31 and 0 < height < 2**31):
                return False
        elif chunk_type == b"IHDR":
            return False

        if chunk_type == b"IDAT" and length:
            has_image_data = True
        if chunk_type == b"IEND":
            return has_image_data and length == 0 and end + 4 == len(content)
        offset = end + 4
    return False


def _valid_jpeg(content: bytes) -> bool:
    if not (content.startswith(b"\xff\xd8") and content.endswith(b"\xff\xd9")):
        return False

    end = len(content) - 2
    offset = 2
    has_frame = False
    while offset < end and content[offset] == 0xFF:
        while offset < end and content[offset] == 0xFF:
            offset += 1
        if offset + 3 > end:
            return False
        marker = content[offset]
        length, = struct.unpack_from(">H", content, offset + 1)
        segment_end = offset + 1 + length
        if length < 2 or segment_end > end or marker < 0xC0 or 0xD0 <= marker <= 0xD9:
            return False
        if marker in JPEG_FRAME_MARKERS:
            if length < 11:
                return False
            height, width, components = struct.unpack_from(">HHB", content, offset + 4)
            if not (width and height and components and length == 8 + 3 * components):
                return False
            has_frame = True
        if marker == 0xDA:
            components = content[offset + 3] if length >= 3 else 0
            return bool(has_frame and components and length == 6 + 2 * components
                        and segment_end < end)
        offset = segment_end
    return False


MEDIA_CHECKS = {
    "application/pdf": _valid_pdf,
    "image/png": _valid_png,
    "image/jpeg": _valid_jpeg,
}


def load_document(path: Path, max_bytes: int = DEFAULT_MAX_BYTES) -> Document:
    """Read a regular file once and validate the bytes supplied to the provider."""
    media_type = MEDIA_TYPES.get(path.suffix.lower())
    if media_type is None:
        raise InputError("unsupported_format", "Use a PDF, PNG, JPG, or JPEG file.")

    try:
        if not path.is_file():
            raise InputError("input_unreadable", "The input must be a readable regular file.")
        with path.open("rb") as source:
            content = source.read(max_bytes + 1)
    except OSError as error:
        raise InputError("input_unreadable", "The input could not be read.") from error

    if len(content) > max_bytes:
        raise InputError("input_too_large", f"The input exceeds {max_bytes} bytes.")
    if not MEDIA_CHECKS[media_type](content):
        raise InputError("invalid_media", "The input is empty, damaged, or mismatches its extension.")
    return Document(content, media_type)
