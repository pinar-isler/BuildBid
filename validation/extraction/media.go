package extraction

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const DefaultMaxBytes int64 = 20 * 1024 * 1024

type Document struct {
	Content   []byte
	MediaType string
	SHA256    string
}

type InputError struct {
	Code    string
	Message string
}

func (e *InputError) Error() string { return e.Message }

var formats = map[string]struct {
	mime  string
	valid func([]byte) bool
}{
	".pdf":  {"application/pdf", validPDF},
	".png":  {"image/png", validPNG},
	".jpg":  {"image/jpeg", validJPEG},
	".jpeg": {"image/jpeg", validJPEG},
}

// LoadDocument reads and hashes the same structurally checked bytes the
// provider will receive.
func LoadDocument(path string, maxBytes int64) (Document, error) {
	if maxBytes < 1 {
		return Document{}, &InputError{"input_too_large", "max_bytes must be a positive integer."}
	}
	ext := filepath.Ext(path)
	format, ok := formats[strings.ToLower(ext)]
	if !ok || ext == filepath.Base(path) {
		return Document{}, &InputError{"unsupported_format", "Use a PDF, PNG, JPG, or JPEG file."}
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Document{}, &InputError{"input_unreadable", "The input must be a readable regular file."}
	}
	file, err := os.Open(path)
	if err != nil {
		return Document{}, &InputError{"input_unreadable", "The input could not be read."}
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return Document{}, &InputError{"input_unreadable", "The input could not be read."}
	}
	readLimit := maxBytes
	if maxBytes < math.MaxInt64 {
		readLimit++
	}
	data, err := io.ReadAll(io.LimitReader(file, readLimit))
	if err != nil {
		return Document{}, &InputError{"input_unreadable", "The input could not be read."}
	}
	if int64(len(data)) > maxBytes {
		return Document{}, &InputError{"input_too_large", fmt.Sprintf("The input exceeds %d bytes.", maxBytes)}
	}
	if !format.valid(data) {
		return Document{}, &InputError{"invalid_media", "The input is empty, damaged, or mismatches its extension."}
	}
	return Document{Content: data, MediaType: format.mime, SHA256: documentHash(data)}, nil
}

func documentHash(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

var (
	pdfHeader  = regexp.MustCompile(`^%PDF-(?:1\.[0-7]|2\.0)[\r\n]`)
	pdfTrailer = regexp.MustCompile(`startxref[\t\n\v\f\r ]+([0-9]{1,10})[\t\n\v\f\r ]+%%EOF[\t\n\f\r \x00]*\z`)
	pdfXref    = regexp.MustCompile(`^(?:xref[\t\r\n ]|[0-9]+[\t\n\v\f\r ]+[0-9]+[\t\n\v\f\r ]+obj\b)`)
)

func validPDF(data []byte) bool {
	if !pdfHeader.Match(data) {
		return false
	}
	window := max(0, len(data)-4096)
	match := pdfTrailer.FindSubmatchIndex(data[window:])
	if match == nil {
		return false
	}
	offset, err := strconv.ParseInt(string(data[window+match[2]:window+match[3]]), 10, 64)
	if err != nil || offset >= int64(window+match[0]) {
		return false
	}
	return pdfXref.Match(data[offset:min(offset+64, int64(len(data)))])
}

const pngSignature = "\x89PNG\r\n\x1a\n"

func validPNG(data []byte) bool {
	if !bytes.HasPrefix(data, []byte(pngSignature)) {
		return false
	}
	hasImageData := false
	for offset := len(pngSignature); len(data)-offset >= 12; {
		length := int64(binary.BigEndian.Uint32(data[offset:]))
		if length > int64(len(data)-offset-12) {
			return false
		}
		chunkType := string(data[offset+4 : offset+8])
		end := offset + 8 + int(length)
		if crc32.ChecksumIEEE(data[offset+4:end]) != binary.BigEndian.Uint32(data[end:]) {
			return false
		}
		if offset == len(pngSignature) {
			if chunkType != "IHDR" || length != 13 {
				return false
			}
			width := binary.BigEndian.Uint32(data[offset+8:])
			height := binary.BigEndian.Uint32(data[offset+12:])
			if width == 0 || height == 0 || width >= 1<<31 || height >= 1<<31 {
				return false
			}
		} else if chunkType == "IHDR" {
			return false
		}
		if chunkType == "IDAT" && length != 0 {
			hasImageData = true
		}
		if chunkType == "IEND" {
			return hasImageData && length == 0 && end+4 == len(data)
		}
		offset = end + 4
	}
	return false
}

func validJPEG(data []byte) bool {
	if !bytes.HasPrefix(data, []byte{0xff, 0xd8}) || !bytes.HasSuffix(data, []byte{0xff, 0xd9}) {
		return false
	}
	end := len(data) - 2
	hasFrame := false
	for offset := 2; offset < end && data[offset] == 0xff; {
		for offset < end && data[offset] == 0xff {
			offset++
		}
		if offset+3 > end {
			return false
		}
		marker := data[offset]
		length := int(binary.BigEndian.Uint16(data[offset+1:]))
		segmentEnd := offset + 1 + length
		if length < 2 || segmentEnd > end || marker < 0xc0 || (marker >= 0xd0 && marker <= 0xd9) {
			return false
		}
		switch marker {
		case 0xc0, 0xc1, 0xc2, 0xc3, 0xc5, 0xc6, 0xc7, 0xc9, 0xca, 0xcb, 0xcd, 0xce, 0xcf:
			if length < 11 {
				return false
			}
			height := binary.BigEndian.Uint16(data[offset+4:])
			width := binary.BigEndian.Uint16(data[offset+6:])
			components := int(data[offset+8])
			if height == 0 || width == 0 || components == 0 || length != 8+3*components {
				return false
			}
			hasFrame = true
		case 0xda:
			if !hasFrame || length < 3 {
				return false
			}
			components := int(data[offset+3])
			return components != 0 && length == 6+2*components && segmentEnd < end
		}
		offset = segmentEnd
	}
	return false
}
