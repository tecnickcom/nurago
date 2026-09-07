/*
Package encode serializes and deserializes values crossing system boundaries
such as databases, queues, caches, and RPC payloads.

The package supports two main modes:

- Gob + Base64 encoding for arbitrary Go values
- JSON + Base64 encoding for interoperable text-based payloads

Caveats:

  - Decoding reads a single encoded value; any trailing bytes are ignored.
  - The reader- and buffer-based decoders do not bound their input. When
    decoding from an untrusted source, wrap the reader with io.LimitReader.
  - Gob is not designed to decode adversarial input; prefer the JSON family
    (Serialize/Deserialize) for untrusted, interoperable payloads.
  - The JSON encoding appends a trailing newline, which is included in the
    encoded output.

# When To Use

  - The same payload shape moves between a database, a queue, and a cache.
  - You want one encoding decision rather than one per call site.
*/
package encode

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Base64EncodeString returns the Base64 representation of s.
//
// Use it when text-safe transport is required for arbitrary bytes.
func Base64EncodeString(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// Base64DecodeString decodes the Base64 string s into its raw bytes.
//
// It is the inverse of Base64EncodeString. The decoded bytes need not be valid
// UTF-8, so they are returned as a byte slice.
func Base64DecodeString(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	return b, nil
}

// Base64Encoder wraps w with a streaming Base64 encoder.
//
// The caller should close the returned writer to flush buffered output.
func Base64Encoder(w io.Writer) io.WriteCloser {
	return base64.NewEncoder(base64.StdEncoding, w)
}

// Base64Decoder wraps r with a streaming Base64 decoder.
//
// It is the read-side counterpart to Base64Encoder.
func Base64Decoder(r io.Reader) io.Reader {
	return base64.NewDecoder(base64.StdEncoding, r)
}

// GobEncoder gob-encodes data into enc and closes enc.
//
// It centralizes gob encoding and close handling for stream-based pipelines.
func GobEncoder(enc io.WriteCloser, data any) error {
	err := gob.NewEncoder(enc).Encode(data)
	if err != nil {
		return errors.Join(fmt.Errorf("gob: %w", err), enc.Close())
	}

	return enc.Close() //nolint:wrapcheck
}

// JSONEncoder JSON-encodes data into enc and closes enc.
//
// It is useful for stream-friendly JSON pipelines with explicit close semantics.
func JSONEncoder(enc io.WriteCloser, data any) error {
	err := json.NewEncoder(enc).Encode(data)
	if err != nil {
		return errors.Join(fmt.Errorf("JSON: %w", err), enc.Close())
	}

	return enc.Close() //nolint:wrapcheck
}

// ByteEncode encodes data as gob+Base64 bytes.
//
// This format is convenient for binary channels while still staying text-safe.
func ByteEncode(data any) ([]byte, error) {
	buf, err := BufferEncode(data)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// ByteDecode decodes gob+Base64 bytes into data.
//
// data must be a pointer to the destination type.
func ByteDecode(msg []byte, data any) error {
	return BufferDecode(bytes.NewReader(msg), data)
}

// Encode encodes data as gob+Base64 string.
//
// It is useful for storing typed payloads in text-only fields.
func Encode(data any) (string, error) {
	buf, err := BufferEncode(data)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

// Decode decodes a gob+Base64 string into data.
//
// data must be a pointer to the destination type. Only the first encoded value
// is read; any trailing bytes are ignored.
func Decode(msg string, data any) error {
	return BufferDecode(strings.NewReader(msg), data)
}

// Serialize encodes data as JSON+Base64 string.
//
// Choose this over Encode when interoperability with non-Go systems matters.
func Serialize(data any) (string, error) {
	buf, err := BufferSerialize(data)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

// Deserialize decodes a JSON+Base64 string into data.
//
// data must be a pointer to the destination type. Only the first encoded value
// is read; any trailing bytes are ignored.
func Deserialize(msg string, data any) error {
	return BufferDeserialize(strings.NewReader(msg), data)
}

// ByteSerialize encodes data as JSON+Base64 bytes.
func ByteSerialize(data any) ([]byte, error) {
	buf, err := BufferSerialize(data)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// ByteDeserialize decodes JSON+Base64 bytes into data.
//
// data must be a pointer to the destination type.
func ByteDeserialize(msg []byte, data any) error {
	return BufferDeserialize(bytes.NewReader(msg), data)
}

// BufferEncode encodes data as gob+Base64 and returns an in-memory buffer.
//
// It is a single-buffer building block reused by Encode and ByteEncode.
func BufferEncode(data any) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}

	err := GobEncoder(Base64Encoder(buf), data)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	return buf, nil
}

// BufferDecode reads gob+Base64 content from reader into data.
//
// data must be a pointer to the destination type. Only the first encoded value
// is read; any trailing bytes are ignored. When reading from an untrusted
// source, wrap reader with io.LimitReader to bound the input.
func BufferDecode(reader io.Reader, data any) error {
	err := gob.NewDecoder(Base64Decoder(reader)).Decode(data)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	return nil
}

// BufferSerialize encodes data as JSON+Base64 and returns an in-memory buffer.
//
// It is a single-buffer building block reused by Serialize and ByteSerialize.
func BufferSerialize(data any) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}

	err := JSONEncoder(Base64Encoder(buf), data)
	if err != nil {
		return nil, fmt.Errorf("serialize: %w", err)
	}

	return buf, nil
}

// BufferDeserialize reads JSON+Base64 content from reader into data.
//
// data must be a pointer to the destination type. Only the first encoded value
// is read; any trailing bytes are ignored. When reading from an untrusted
// source, wrap reader with io.LimitReader to bound the input.
func BufferDeserialize(reader io.Reader, data any) error {
	err := json.NewDecoder(Base64Decoder(reader)).Decode(data)
	if err != nil {
		return fmt.Errorf("deserialize: %w", err)
	}

	return nil
}
