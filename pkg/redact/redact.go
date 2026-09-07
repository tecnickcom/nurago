/*
Package redact removes secrets from log lines and HTTP dumps before they are
emitted.

Every pattern is matched in a single pass over the input, so the cost does not
grow with the number of patterns enabled.

# API

All redaction runs through a [Redactor]. [Default] returns the shared,
zero-configuration instance that suits most callers:

	safe := redact.Default().String(rawPayload)

The entry points are [Redactor.String], [Redactor.Bytes], [Redactor.AppendTo],
[Redactor.BytesToString] and [Redactor.Pooled]; they all run the same redaction
engine and differ only in input and output handling.

# Custom Redactors

[New] builds an independent instance when a component needs different behavior:

	re := redact.New(
		redact.WithMarker("#REDACTED#"),          // custom placeholder
		redact.WithLuhnCheck(true),                // instance-scoped Luhn gate
		redact.WithExtraTokens("floof"),           // company-specific key tokens
		redact.WithoutTokens("amount", "balance"), // keep fintech fields readable
		redact.WithoutRules(redact.RuleCards),     // disable whole rule classes
	)
	safe := re.String(rawPayload)

Instances are immutable after construction and safe for concurrent use. A method
value such as re.BytesToString satisfies the redact-function option of the
httpclient, httpserver, and httpreverseproxy packages, which default to
[Default] when the option is omitted.

# Disabling Redaction

[WithoutRules] switches off individual rule classes while the rest keep
redacting. To disable redaction outright, pass [InsecureNoRedaction] as the
redact function: it returns its input verbatim, so secrets are logged in the
clear, and it is named to make that choice conspicuous wherever it appears.
Redaction is never lost by omission (an unset option falls back to [Default],
and a nil function is ignored); it is lost only by naming that bypass.

# What It Redacts

Each call applies multiple redaction rules in a single pass:

  - HTTP headers whose name is sensitive (`Authorization`,
    `Proxy-Authorization`, `Cookie`, `Set-Cookie`, `X-Api-Key`,
    `X-Auth-Token`, `X-Amz-Security-Token`, ...), preserving the header name
    while replacing the whole value. Sensitivity uses the same tokenized
    keyword check as JSON and URL-encoded keys, including a shared allowlist of
    well-known keys that tokenize to a sensitive keyword but never carry a
    secret (CSP/HSTS response headers, the `Access-Control-Allow-Credentials`
    and `WWW-Authenticate`/`Proxy-Authenticate` headers, and the
    `securityContext`/`securityGroups` structural fields), which stay visible
    on every surface.
  - JSON key/value pairs whose key name contains sensitive keywords
    (authentication/session, crypto markers, legal-signing, financial, and PII
    keyword groups). When the value is a JSON object or array, the entire
    nested container is replaced with the marker.
  - URL-encoded key/value pairs with matching sensitive key names.
  - XML/HTML elements with sensitive names and flat text content
    (`<password>...</password>`); sensitive XML attributes are covered by the
    URL-encoded rule.
  - URL userinfo passwords anywhere in the text
    (`postgres://user:secret@host` -> `postgres://user:***@host`), catching
    bare DSNs in error messages.
  - JWT/JWE compact tokens (`eyJ...`) in free text, query strings, and JSON
    values.
  - Well-known vendor credential literals by their unmistakable prefixes:
    GitHub (`ghp_`, `github_pat_`, ...), Slack (`xoxb-`, ...), Stripe
    (`sk_live_`, `whsec_`, ...), OpenAI/Anthropic (`sk-`), Hugging Face
    (`hf_`), SendGrid (`SG.`), AWS access key ids (`AKIA`, `ASIA`), Google
    (`AIza`), GitLab (`glpat-`), DigitalOcean, Docker Hub, and Shopify
    tokens.
  - PEM `-----BEGIN ... PRIVATE KEY-----` blocks: the base64 body is replaced
    with a single marker line (public blocks such as CERTIFICATE stay
    visible). Blocks embedded mid-line (e.g. a JSON string carrying a blob
    with escaped `\n` sequences) are redacted too.
  - Credit-card numbers: contiguous digit runs, and runs grouped by single
    spaces or dashes (`4012 8888 8888 1881`), bounded by non-word characters.

# Key Matching

Keyword matching is token-exact after normalization: camelCase, snake_case,
kebab-case, and acronym runs all tokenize (`apiKey`, `api_key`, `API-KEY` and
`APIKey` all yield `api` + `key`). Two bounded retries widen this:

  - a trailing digit run is stripped, so a numbered field matches its base
    (`password2`, `cvv2`, `key1`); a digit rarely ends an ordinary key word, so
    this retry runs against the whole keyword set. A key-size label whose stem is
    a keyword (`rsa2048`, `dsa1024`) is redacted as a corollary; it is a false
    positive on non-secret metadata, never a leak.
  - a trailing plural `s` is stripped, but only the strong roots below are
    retried, so `tokens`, `apikeys` and `newpasswords` redact while plurals of
    ordinary-word keywords (`keys`, a JWKS array; `cells`; `accounts`;
    `payments`) stay visible.

A few names are sensitive only as a two-word pair, where neither word is a
keyword alone: `first`/`last` + `name`, `national` + `id`, and `connection` +
`string`. So `firstName`, `nationalId`/`national_id` and
`connectionString`/`connection_string` all match, while `international` and
`connectionTimeout` do not.

A closed compound (an all-lowercase glued name with no boundary to split on, as
HTML forms use, e.g. `<input name="newpassword">`) matches in two cases only:

  - it is one of the enumerated keywords (`apikey`, `passphrase`,
    `clientsecret`, `connectionstring`, `masterkey`, `sessioncookie`, ...); or
  - it ends in one of a short list of unambiguous roots: `password`, `passwd`,
    `pwd`, `passphrase`, `passcode`, `secret`, `token`, `signature`,
    `credential`, `creditcard`, `debitcard`, `cardnumber`, and the `*key`
    family (`apikey`, `accesskey`, `secretkey`, `sessionkey`, `signingkey`,
    `encryptionkey`, `authkey`, `privatekey`, `privkey`, `sshkey`). So
    `newpassword`, `oldpassword` and `awssecretkey` are redacted.

The root list is deliberately short, and matching is never a plain substring
search: near-miss words like "monkey" never match `key`, and "wildcard" never
matches `card`. House-style names outside these rules are best added with
[WithExtraTokens].

CRLF line endings (as produced by httputil.DumpRequest and DumpResponse) are
preserved. All matched sensitive values are replaced with a constant marker so
output remains structurally useful while hiding private content. Redaction is
convergent: re-redacting output never reveals more, is byte-stable in a single
pass on well-formed input, and reaches a fixed point after at most one extra
pass on pathological (structurally ambiguous) input, so layered redaction does
not keep mangling logs.

# Credit-Card Detection and the Optional Luhn Gate

By default, any 13-19 digit run (contiguous, or grouped by single spaces or
dashes) that matches a known card prefix and network length (Visa, Mastercard,
Amex, Discover, Diners, JCB, UnionPay, ...) and is bounded by non-word
characters is redacted. This is deliberate over-redaction: it is the safe
default and may also redact unrelated numeric identifiers that happen to share
a card prefix and length. Grouped-format detection excludes the 14-digit
Diners and the legacy 15-digit ranges (JCB 2131, Diners 1800), whose prefixes
collide with common phone-number formats ("1 800 555 0199 1234"); those still
match as contiguous runs.

Maestro is handled conservatively: its well-known 4-digit issuer prefixes
(5018, 5020, 5038, 5893, 6304, 6759, 6761-6763) are detected at 16-19 digits by
default, while the short 12-15 digit Maestro forms are only detected when the
Luhn gate below is enabled, because a short prefix-and-length match alone would
collide with far too many ordinary identifiers. The broader Maestro ranges are
not enrolled as Maestro, though several of those prefixes are still redacted
under other networks (56-57 as Mastercard, 62 as UnionPay, 65 as Discover); only
50, 58, 59, 61, 63, 66, 68 and 69 (minus the well-known IINs) stay visible at 16
digits.

Callers that prefer fewer false positives can enable an additional Luhn-checksum
gate with [WithLuhnCheck]. When enabled, a digit run is only redacted if it
matches a known prefix AND passes the Luhn checksum:

	re := redact.New(redact.WithLuhnCheck(true))
	safe := re.String(rawPayload)

The gate is instance-scoped and fixed at construction, and defaults to off. The
shared [Default] instance always runs with it off; a component that wants it
must build its own [Redactor] with [New]. Enabling it may cause malformed or
non-Luhn test numbers to be left visible.

# Usage

	re := redact.Default()

	safe := re.String(rawPayload)
	logger.Info("request", "payload", safe)

For high-throughput paths, reuse an output buffer to avoid per-call allocations:

	var dst []byte
	for _, payload := range payloads {
		dst = re.AppendTo(dst, payload)
		logger.Info("request", "payload", string(dst))
	}

# Important Notes

This package is best-effort pattern redaction, not a formal data-loss
prevention system. Always treat output as potentially sensitive and combine this
with least-privilege logging practices and structured logging controls.

The recognized input shapes are the ones listed above (HTTP headers, JSON,
URL-encoded pairs, XML, DSNs, and free-text tokens). Go's native rendering of
composite values is not among them: neither `fmt.Sprintf("%+v", req)` nor
`map[password:secret]` is redacted, because they carry no `=`, `:` or quoted-key
structure the engine can anchor on. Redact the fields, or marshal to JSON first.

multipart/form-data body fields are also not redacted: the field name lives on a
`Content-Disposition` line and its value sits on a later line separated by a
blank line, so the two are structurally decoupled across lines and the engine
has nothing to anchor a single-pass rule on. Redact multipart field values
before logging a captured body.

The marker is assumed to be inert: a URL-encoded value whose text begins with
the marker followed by a separator (`password=*** tail`) is read as already
redacted and its tail is left alone. That is what keeps a second redaction pass
from eating the rest of a logfmt line (`password=*** user=bob`); the cost is
that an input value that literally starts with `*** ` keeps its tail visible.

An unquoted URL-encoded value ends at a structural byte (`&`, `,`, `;`, `<`,
`>`, quote, or a line break) so redaction cannot consume a following pair or the
document's structure. A secret that contains one of those bytes raw therefore
keeps the tail after it visible (`password=Pa,ssword1` -> `password=***,ssword1`).
Real form bodies percent-encode those bytes; this only surfaces in hand-written
`key=value` text. Quote the value (`password="Pa,ssword1"`) and it is redacted
whole.

Deliberate non-goals (too collision-prone to match on shape alone): Telegram
bot tokens (digits:base64 collides with host:port shapes) and bare
"Basic <base64>" blobs in prose (header-positioned Basic credentials are
covered by the Authorization rule); obsolete obs-fold header continuation
lines.

# When To Use

  - Request and response dumps reach your logs and may contain credentials.
  - You need coverage across headers, JSON bodies, form data, DSNs, JWTs, PEM
    keys, and card numbers at once.
  - Redaction runs on a hot path and must not allocate per pattern.
*/
package redact
