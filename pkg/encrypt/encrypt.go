/*
Package encrypt encrypts and decrypts data for transport and storage using
AES-GCM authenticated encryption.

It protects application payloads moving between systems such as databases,
queues, caches, or external services.

This package uses AES-GCM authenticated encryption with a random nonce prefixed
to the ciphertext. It provides both raw byte-level APIs and convenience helpers
that serialize arbitrary values with gob or JSON before encryption.

# Security and caveats

  - Random-nonce message limit: each call generates a fresh 96-bit random nonce.
    With random nonces the number of messages that may safely be encrypted under a
    single key is bounded by the birthday paradox (see NIST SP 800-38D). Rotate
    keys well before ~2^32 messages per key to keep the nonce-collision probability
    negligible. A nonce collision under the same key breaks both confidentiality
    and authentication.
  - Nonce uniqueness depends entirely on the randomness source. [Encrypt] uses
    [crypto/rand.Reader]. Override it (via [EncryptWith] and [WithRandReader]) only
    in tests; a non-cryptographic or repeating reader causes nonce reuse.
  - The gob helpers ([ByteEncryptAny]/[ByteDecryptAny] and their string wrappers)
    decode with [encoding/gob], which is not designed for adversarial input.
    Because the payload is authenticated before decoding, only data produced by a
    holder of the key ever reaches the decoder; even so, prefer the JSON family
    (the *SerializeAny helpers) for cross-language or lower-trust payloads.
  - The Base64 output uses standard encoding (RFC 4648 with '+' and '/'), which is
    not URL- or filename-safe. Re-encode at the call site if you need to embed the
    payload in a URL or path.
  - All exported functions are stateless and safe for concurrent use.

# When To Use

  - A field must be encrypted before it reaches the database or a queue.
  - You want authenticated encryption without assembling the pieces yourself.
*/
package encrypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrInvalidInputSize is returned by [Decrypt] and [DecryptWith] when the payload
// is shorter than the AES-GCM nonce, so it cannot contain a nonce plus ciphertext.
var ErrInvalidInputSize = errors.New("encrypt: input shorter than nonce size")

// config holds the resolved options for an encryption or decryption call.
//
// The zero value uses a nil random reader, which [EncryptWith] interprets as
// crypto/rand.Reader, and a nil AAD. This keeps Encrypt non-failing in production
// while allowing tests to inject a deterministic or failing reader via Option,
// without any runtime-mutated package global.
type config struct {
	randReader io.Reader
	aad        []byte
}

// Option customizes the behavior of [EncryptWith] and [DecryptWith].
//
// Options are additive: existing Encrypt/Decrypt signatures are unchanged and
// always use the secure default reader (crypto/rand.Reader) with no AAD.
type Option func(*config)

// WithRandReader overrides the random source used to generate the AES-GCM nonce.
//
// SECURITY: the reader MUST be a cryptographically secure source of unique bytes.
// Reusing a nonce under the same key breaks AES-GCM confidentiality and
// authentication, so this option is intended for tests only. Production code must
// use [Encrypt] (or [EncryptWith] without this option), which reads from
// crypto/rand.Reader. It has no effect on [DecryptWith].
func WithRandReader(r io.Reader) Option {
	return func(c *config) {
		c.randReader = r
	}
}

// WithAAD binds additional authenticated data (AAD) to the ciphertext.
//
// The AAD is authenticated but not encrypted: the exact same value must be
// supplied to [DecryptWith] or decryption fails. Use it to bind contextual data
// (such as a record ID or schema version) to a payload. The high-level
// Any/Serialize helpers do not expose AAD; use [EncryptWith]/[DecryptWith] for it.
func WithAAD(aad []byte) Option {
	return func(c *config) {
		c.aad = aad
	}
}

// newAESGCM creates an AES-GCM AEAD from key.
//
// key must be a valid AES key length (16, 24, or 32 bytes).
func newAESGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	return cipher.NewGCM(block) //nolint:wrapcheck
}

// Encrypt seals msg with AES-GCM and prepends the random nonce.
//
// key must be 16, 24, or 32 bytes for AES-128/192/256.
// The output is self-contained: nonce || ciphertext.
// The nonce is generated from crypto/rand.Reader.
func Encrypt(key, msg []byte) ([]byte, error) {
	return EncryptWith(key, msg)
}

// EncryptWith behaves like [Encrypt] but accepts options that customize the
// random source used for nonce generation (see [WithRandReader]) and bind
// additional authenticated data (see [WithAAD]).
//
// Without options it is identical to Encrypt and uses crypto/rand.Reader.
func EncryptWith(key, msg []byte, opts ...Option) ([]byte, error) {
	cfg := &config{}
	for _, applyOpt := range opts {
		applyOpt(cfg)
	}

	aesgcm, err := newAESGCM(key)
	if err != nil {
		return nil, err
	}

	reader := cfg.randReader
	if reader == nil {
		reader = rand.Reader
	}

	nonce := make([]byte, aesgcm.NonceSize())

	_, err = io.ReadFull(reader, nonce)
	if err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	return aesgcm.Seal(nonce, nonce, msg, cfg.aad), nil
}

// Decrypt opens a nonce-prefixed AES-GCM payload produced by [Encrypt].
//
// key must match the key used during encryption.
func Decrypt(key, msg []byte) ([]byte, error) {
	return DecryptWith(key, msg)
}

// DecryptWith behaves like [Decrypt] but accepts options. Only [WithAAD] is
// honored: the AAD must match the value passed to [EncryptWith] or decryption
// fails. [WithRandReader] has no effect on decryption.
func DecryptWith(key, msg []byte, opts ...Option) ([]byte, error) {
	cfg := &config{}
	for _, applyOpt := range opts {
		applyOpt(cfg)
	}

	aesgcm, err := newAESGCM(key)
	if err != nil {
		return nil, err
	}

	ns := aesgcm.NonceSize()
	if len(msg) < ns {
		return nil, ErrInvalidInputSize
	}

	return aesgcm.Open(nil, msg[:ns], msg[ns:], cfg.aad) //nolint:wrapcheck
}

// byteEncryptEncoded encrypts data and returns Base64-encoded ciphertext bytes.
func byteEncryptEncoded(key []byte, data []byte) ([]byte, error) {
	msg, err := Encrypt(key, data)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}

	dst := make([]byte, base64.StdEncoding.EncodedLen(len(msg)))
	base64.StdEncoding.Encode(dst, msg)

	return dst, nil
}

// byteDecryptEncoded decodes Base64 payload bytes and decrypts them.
func byteDecryptEncoded(key, msg []byte) ([]byte, error) {
	dst := make([]byte, base64.StdEncoding.DecodedLen(len(msg)))

	n, err := base64.StdEncoding.Decode(dst, msg)
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}

	return Decrypt(key, dst[:n])
}

// ByteEncryptAny gob-encodes data, encrypts it, and returns Base64 bytes.
//
// This helper is useful for encrypting arbitrary Go values for transport.
func ByteEncryptAny(key []byte, data any) ([]byte, error) {
	buf := &bytes.Buffer{}

	err := gob.NewEncoder(buf).Encode(data)
	if err != nil {
		return nil, fmt.Errorf("encode gob: %w", err)
	}

	return byteEncryptEncoded(key, buf.Bytes())
}

// ByteDecryptAny decrypts Base64 bytes produced by ByteEncryptAny into data.
//
// data must be a pointer to the destination type.
func ByteDecryptAny(key, msg []byte, data any) error {
	dec, err := byteDecryptEncoded(key, msg)
	if err != nil {
		return err
	}

	err = gob.NewDecoder(bytes.NewBuffer(dec)).Decode(data)
	if err != nil {
		return fmt.Errorf("decode gob: %w", err)
	}

	return nil
}

// EncryptAny wraps ByteEncryptAny and returns a string payload.
func EncryptAny(key []byte, data any) (string, error) {
	b, err := ByteEncryptAny(key, data)
	if err != nil {
		return "", err
	}

	return string(b), nil
}

// DecryptAny wraps ByteDecryptAny for string input payloads.
func DecryptAny(key []byte, msg string, data any) error {
	return ByteDecryptAny(key, []byte(msg), data)
}

// ByteEncryptSerializeAny JSON-encodes data, encrypts it, and returns Base64 bytes.
//
// Use this helper for cross-language payloads where JSON interoperability is required.
func ByteEncryptSerializeAny(key []byte, data any) ([]byte, error) {
	buf := &bytes.Buffer{}

	err := json.NewEncoder(buf).Encode(data)
	if err != nil {
		return nil, fmt.Errorf("encode json: %w", err)
	}

	return byteEncryptEncoded(key, buf.Bytes())
}

// ByteDecryptSerializeAny decrypts Base64 bytes into JSON-decoded data.
//
// data must be a pointer to the destination type.
func ByteDecryptSerializeAny(key, msg []byte, data any) error {
	dec, err := byteDecryptEncoded(key, msg)
	if err != nil {
		return err
	}

	err = json.NewDecoder(bytes.NewBuffer(dec)).Decode(data)
	if err != nil {
		return fmt.Errorf("decode json: %w", err)
	}

	return nil
}

// EncryptSerializeAny wraps ByteEncryptSerializeAny and returns a string payload.
func EncryptSerializeAny(key []byte, data any) (string, error) {
	b, err := ByteEncryptSerializeAny(key, data)
	if err != nil {
		return "", err
	}

	return string(b), nil
}

// DecryptSerializeAny wraps ByteDecryptSerializeAny for string input payloads.
func DecryptSerializeAny(key []byte, msg string, data any) error {
	return ByteDecryptSerializeAny(key, []byte(msg), data)
}
