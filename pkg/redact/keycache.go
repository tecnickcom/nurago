package redact

import (
	"strings"
	"sync"
	"unicode"
)

// normalizeKey converts a key name to a tokenized lowercase form suitable for
// boundary-aware keyword checks (e.g., camelCase -> snake_case tokens).
// Uppercase acronym runs followed by a lowercase letter are split before the
// last uppercase letter (e.g., APIKey -> api_key, JWTToken -> jwt_token).
//
//nolint:gocognit,gocyclo,cyclop
func normalizeKey(s string) string {
	var b strings.Builder
	b.Grow(len(s) * 2)

	runes := []rune(s)
	prevIsLowerOrDigit := false
	prevIsUpper := false
	prevIsUnderscore := true

	for i, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			isUpper := unicode.IsUpper(r)
			nextIsLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])

			if isUpper && !prevIsUnderscore && (prevIsLowerOrDigit || (prevIsUpper && nextIsLower)) {
				b.WriteByte('_')
			}

			b.WriteRune(unicode.ToLower(r))

			prevIsLowerOrDigit = unicode.IsLower(r) || unicode.IsDigit(r)
			prevIsUpper = isUpper
			prevIsUnderscore = false

			continue
		}

		if !prevIsUnderscore {
			b.WriteByte('_')
		}

		prevIsLowerOrDigit = false
		prevIsUpper = false
		prevIsUnderscore = true
	}

	return strings.Trim(b.String(), "_")
}

const (
	// sensitiveKeyCacheMaxEntries bounds the key-sensitivity memoization cache.
	// This avoids unbounded memory growth with high-cardinality inputs.
	sensitiveKeyCacheMaxEntries = 4096

	// sensitiveKeyCacheEvictBatch controls how many entries are dropped when the
	// cache is full, reducing lock contention versus evicting one-by-one.
	sensitiveKeyCacheEvictBatch = 512
)

// Sensitive tokens are precomputed once and matched against normalized key tokens.
var sensitiveTokens = map[string]struct{}{ //nolint:gochecknoglobals
	"acc":              {},
	"accesskey":        {},
	"accesstoken":      {},
	"account":          {},
	"address":          {},
	"amount":           {},
	"apikey":           {},
	"apisecret":        {},
	"appsecret":        {},
	"attestation":      {},
	"auth":             {},
	"authcookie":       {},
	"authenticate":     {},
	"authkey":          {},
	"authorization":    {},
	"authtoken":        {},
	"autograph":        {},
	"bal":              {},
	"balance":          {},
	"bearer":           {},
	"bearertoken":      {},
	"bill":             {},
	"birth":            {},
	"card":             {},
	"cardnumber":       {},
	"cc":               {},
	"cell":             {},
	"cert":             {},
	"checksum":         {},
	"clientsecret":     {},
	"connectionstring": {},
	"cookie":           {},
	"cred":             {},
	"credential":       {},
	"credentials":      {},
	"creditcard":       {},
	"csc":              {},
	"csrf":             {},
	"csrftoken":        {},
	"cv2":              {},
	"cvc":              {},
	"cvn":              {},
	"cvv":              {},
	"dbpassword":       {},
	"debitcard":        {},
	"deploykey":        {},
	"dob":              {},
	"dsa":              {},
	"dsn":              {},
	"ecdsa":            {},
	"email":            {},
	"encryptionkey":    {},
	"endorse":          {},
	"expiry":           {},
	"fingerprint":      {},
	"gpgkey":           {},
	"hash":             {},
	"hmac":             {},
	"holder":           {},
	"hostkey":          {},
	"htpasswd":         {},
	"iban":             {},
	"idtoken":          {},
	"jsessionid":       {},
	"jwt":              {},
	"key":              {},
	"login":            {},
	"mail":             {},
	"masterkey":        {},
	"mfa":              {},
	"nationalid":       {},
	"nonce":            {},
	"otp":              {},
	"pass":             {},
	"passcode":         {},
	"passphrase":       {},
	"passport":         {},
	"passportnumber":   {},
	"passwd":           {},
	"password":         {},
	"pay":              {},
	"payment":          {},
	"pgpassword":       {},
	"phone":            {},
	"phpsessid":        {},
	"pin":              {},
	"pkcs":             {},
	"pkcs12":           {},
	"postal":           {},
	"privatekey":       {},
	"privkey":          {},
	"proof":            {},
	"pwd":              {},
	"refreshtoken":     {},
	"rootkey":          {},
	"rsa":              {},
	"salt":             {},
	"seal":             {},
	"secret":           {},
	"secretkey":        {},
	"security":         {},
	"sess":             {},
	"session":          {},
	"sessioncookie":    {},
	"sessionid":        {},
	"sessionkey":       {},
	"sessiontoken":     {},
	"sgn":              {},
	"sid":              {},
	"sig":              {},
	"signature":        {},
	"signingkey":       {},
	"signkey":          {},
	"social":           {},
	"sshkey":           {},
	"ssn":              {},
	"swift":            {},
	"tax":              {},
	"tel":              {},
	"telephone":        {},
	"token":            {},
	"totp":             {},
	"xsrf":             {},
	"xsrftoken":        {},
}

// nonSensitiveKeys are well-known keys that tokenize to a sensitive keyword yet
// never carry a secret: CSP/HSTS response headers and the CORS credentials flag
// (the "security"/"credentials" tokens), auth *challenge* headers (the
// "authenticate" token, distinct from the credential-bearing "authorization"),
// and the Kubernetes/AWS security-* structural fields. They are kept visible on
// EVERY surface (headers, JSON, URL-encoded, XML) so a debugging dump stays
// readable; the same tokenized keyword check therefore applies uniformly.
// Entries are stored in normalizeKey form and matched case-insensitively.
var nonSensitiveKeys = map[string]struct{}{ //nolint:gochecknoglobals
	"content_security_policy":             {},
	"content_security_policy_report_only": {},
	"x_content_security_policy":           {},
	"strict_transport_security":           {},
	"access_control_allow_credentials":    {},
	"www_authenticate":                    {},
	"proxy_authenticate":                  {},
	"security_context":                    {},
	"security_group":                      {},
	"security_groups":                     {},
}

// isNonSensitiveKey reports whether key (any spelling) normalizes to a
// nonSensitiveKeys entry. A cheap allocation-free prescreen rejects the vast
// majority of sensitive keys (which contain none of the allowlist substrings)
// before the normalizing lookup, so the common sensitive key pays nothing.
func isNonSensitiveKey(key []byte) bool {
	if !mightBeNonSensitiveKey(key) {
		return false
	}

	return isNonSensitiveNormalizedKey(normalizeKey(string(key)))
}

// mightBeNonSensitiveKey is the prescreen for isNonSensitiveKey: every
// nonSensitiveKeys entry contains one of these lowercase substrings verbatim
// (the security/credentials/authenticate token that made it a false positive),
// so a key lacking all three can never be allowlisted.
func mightBeNonSensitiveKey(key []byte) bool {
	return containsFold(key, "secur") || containsFold(key, "authenticat") || containsFold(key, "credential")
}

// containsFold reports whether key contains the lowercase-ASCII substring sub,
// matched case-insensitively, without allocating.
func containsFold(key []byte, sub string) bool {
	n, m := len(key), len(sub)
	if m > n {
		return false
	}

	for i := 0; i+m <= n; i++ {
		j := 0
		for j < m && lowerASCIIByte(key[i+j]) == sub[j] {
			j++
		}

		if j == m {
			return true
		}
	}

	return false
}

// isNonSensitiveNormalizedKey reports whether an already-normalized key is
// allowlisted as non-secret.
func isNonSensitiveNormalizedKey(normalized string) bool {
	_, ok := nonSensitiveKeys[normalized]

	return ok
}

// Two-word sensitive pairs: a key is sensitive when two adjacent tokens form
// one of these pairs, even though neither word is a sensitive token on its own.
// This catches the natural spellings of names that are otherwise enumerated only
// as a fully-glued token (`nationalid`, `connectionstring`), so `nationalId`,
// `national_id`, `connectionString` and `connection_string` all match.
const (
	pairNone          = iota
	pairFirstLastName // head "first"/"last", tail "name"
	pairNationalID    // head "national", tail "id"
	pairConnectionStr // head "connection", tail "string"
)

// pairHeadKind classifies a token that is the FIRST word of a two-word
// sensitive pair, or pairNone. Dispatched on length so a token pays at most one
// equalsASCIIFold; this runs for every token of every key.
func pairHeadKind(tok []byte) int {
	switch len(tok) {
	case 4:
		if equalsASCIIFold(tok, "last") {
			return pairFirstLastName
		}
	case 5:
		if equalsASCIIFold(tok, "first") {
			return pairFirstLastName
		}
	case 8:
		if equalsASCIIFold(tok, "national") {
			return pairNationalID
		}
	case 10:
		if equalsASCIIFold(tok, "connection") {
			return pairConnectionStr
		}
	}

	return pairNone
}

// matchesPairTail reports whether tok completes the two-word pair opened by a
// head of the given kind.
func matchesPairTail(kind int, tok []byte) bool {
	switch kind {
	case pairFirstLastName:
		return equalsASCIIFold(tok, "name")
	case pairNationalID:
		return equalsASCIIFold(tok, "id")
	case pairConnectionStr:
		return equalsASCIIFold(tok, "string")
	}

	return false
}

// matchesPairString is the string-token form of the pair check, for the
// normalized (non-ASCII) classification path.
func matchesPairString(prev, tok string) bool {
	switch prev {
	case "first", "last":
		return tok == "name"
	case "national":
		return tok == "id"
	case "connection":
		return tok == "string"
	}

	return false
}

// strongSecretTokens are the unambiguous secret-indicating tokens. The labeled
// secret rule ({"name":"<key>","value":...}) classifies its label VALUE against
// this set (plus the glued-compound roots) instead of the full token set, so a
// label naming a real credential (`DB_PASSWORD`, `api_key`, `clientSecret`)
// redacts the sibling value while a weak/financial/PII label token
// (`card`/`pay`/`payment`/`auth`/`account`) does not blank ordinary {name,value}
// data. Roots (password, secret, token, the *key family, ...) are covered
// separately via hasSensitiveRootSuffix; this set adds the strong exact tokens.
var strongSecretTokens = map[string]struct{}{ //nolint:gochecknoglobals
	"apikey": {}, "apisecret": {}, "appsecret": {}, "accesskey": {}, "accesstoken": {},
	"authkey": {}, "authorization": {}, "authtoken": {}, "bearer": {}, "bearertoken": {},
	"clientsecret": {}, "connectionstring": {}, "cred": {}, "credential": {}, "credentials": {},
	"csrf": {}, "csrftoken": {}, "dbpassword": {}, "deploykey": {}, "encryptionkey": {},
	"gpgkey": {}, "hmac": {}, "hostkey": {}, "htpasswd": {}, "idtoken": {},
	"jsessionid": {}, "jwt": {}, "key": {}, "masterkey": {}, "mfa": {},
	"otp": {}, "pass": {}, "passcode": {}, "passphrase": {}, "passwd": {},
	"password": {}, "pgpassword": {}, "phpsessid": {}, "pkcs": {}, "pkcs12": {},
	"privatekey": {}, "privkey": {}, "pwd": {}, "refreshtoken": {}, "rootkey": {},
	"secret": {}, "secretkey": {}, "session": {}, "sessionid": {}, "sessionkey": {},
	"sessiontoken": {}, "signature": {}, "signingkey": {}, "signkey": {}, "sshkey": {},
	"token": {}, "totp": {}, "xsrf": {}, "xsrftoken": {},
}

// sensitiveRoots are the secret words that also match as the tail of a longer
// glued compound. The tokenizer only splits on case and separator boundaries,
// so an all-lowercase closed compound (the shape HTML forms and their
// URL-encoded dumps use, e.g. `<input name="newpassword">`) arrives as a single
// token and would otherwise miss the exact lookup.
//
// The list is deliberately short: an entry qualifies only when no ordinary
// word ends in it, so the suffix rule stays a bounded generalization rather
// than a substring free-for-all. Ambiguous roots are excluded on purpose
// ("key" would match "monkey" and "card" would match "wildcard") and stay
// exact-match only.
//
// Every root must also be an exact token in sensitiveTokens (TestSensitiveRootsAreTokens).
var sensitiveRoots = []string{ //nolint:gochecknoglobals
	"accesskey", "apikey", "authkey", "cardnumber", "credential", "creditcard",
	"debitcard", "encryptionkey", "passcode", "passphrase", "passwd", "password",
	"privatekey", "privkey", "pwd", "secret", "secretkey", "sessionkey",
	"signature", "signingkey", "sshkey", "token",
}

// minGluedCompoundLen is the shortest token the root-suffix rule considers.
// The only roots shorter than 6 bytes are "pwd" (3) and "token"/"passwd" (5/6),
// and the shortest real glued compound of any of them is a 5-byte "pwd" form
// (`dbpwd`, `mypwd`), which does NOT tokenize on its own
// (`normalizeKey("dbpwd") == "dbpwd"`, a single non-sensitive token). Gating at
// 5 keeps those while sparing the many 4-byte ordinary keys of a log line
// ("host", "code", "name", "user") the scan entirely.
const minGluedCompoundLen = 5

// sensitiveRootsByLastByte indexes sensitiveRoots by their final byte, so a
// token whose last byte ends no root is rejected with a single table load.
var sensitiveRootsByLastByte = newSensitiveRootIndex() //nolint:gochecknoglobals

// sensitiveRootSet is the same roots as a set, for the exact-root lookup the
// plural rule uses (see sensitiveToken): stripping a trailing "s" matches only
// the strong roots, never the short exact tokens, so "tokens"/"apikeys" redact
// while "keys"/"cells"/"accounts" (plurals of ordinary-word tokens) stay visible.
var sensitiveRootSet = newSensitiveRootSet() //nolint:gochecknoglobals

// newSensitiveRootSet materializes sensitiveRoots as a lookup set.
func newSensitiveRootSet() map[string]struct{} {
	set := make(map[string]struct{}, len(sensitiveRoots))
	for _, root := range sensitiveRoots {
		set[root] = struct{}{}
	}

	return set
}

// newSensitiveRootIndex buckets the roots by their final byte.
func newSensitiveRootIndex() [256][]string {
	var index [256][]string

	for _, root := range sensitiveRoots {
		last := root[len(root)-1]
		index[last] = append(index[last], root)
	}

	return index
}

// sensitiveKeyMemo is a small concurrent memoization cache for sensitive-key checks.
type sensitiveKeyMemo struct {
	mu   sync.RWMutex
	data map[string]bool
}

// newSensitiveKeyMemo creates an empty memo cache. The backing map (pre-sized
// for the max entry count, ~200 KB) is allocated lazily on the first insertion,
// so an instance that only ever classifies ASCII keys (which never reach the
// memo) carries no cache at all.
func newSensitiveKeyMemo() *sensitiveKeyMemo {
	return &sensitiveKeyMemo{}
}

// get performs a read-locked lookup. Reading a nil (not-yet-allocated) map is a
// safe miss.
func (c *sensitiveKeyMemo) get(k string) (bool, bool) {
	c.mu.RLock()
	v, ok := c.data[k]
	c.mu.RUnlock()

	return v, ok
}

// set inserts or updates a memoized key, allocating the backing map on first
// use. When full, it evicts a small batch of arbitrary entries to keep memory
// bounded.
func (c *sensitiveKeyMemo) set(k string, v bool) {
	c.mu.Lock()
	if c.data == nil {
		c.data = make(map[string]bool, sensitiveKeyCacheMaxEntries)
	}

	if len(c.data) >= sensitiveKeyCacheMaxEntries {
		evicted := 0

		for key := range c.data {
			delete(c.data, key)

			evicted++
			if evicted >= sensitiveKeyCacheEvictBatch {
				break
			}
		}
	}

	c.data[k] = v
	c.mu.Unlock()
}

// isSensitiveKey reports whether a key contains a sensitive token after
// normalization, honoring the instance's extra and dropped tokens. A key that
// matches a sensitive token but is on the nonSensitiveKeys allowlist (CSP/HSTS
// headers, security-* structural fields, ...) is reported non-sensitive on every
// surface; the allowlist check runs only for the sensitive minority, off the
// common non-sensitive-key fast path.
func (re *Redactor) isSensitiveKey(key []byte) bool {
	if result, ok := re.sensitiveKeyASCIIFast(key); ok {
		return result && !isNonSensitiveKey(key)
	}

	k := string(key)
	if v, ok := re.keyMemo.get(k); ok {
		return v
	}

	normalized := normalizeKey(k)
	result := re.isSensitiveNormalizedKeyTokens(normalized) && !isNonSensitiveNormalizedKey(normalized)
	re.keyMemo.set(k, result)

	return result
}

//nolint:cyclop,gocognit,gocyclo
func (re *Redactor) sensitiveKeyASCIIFast(key []byte) (bool, bool) {
	start := -1
	prevIsLowerOrDigit := false
	prevIsUpper := false
	prevIsUnderscore := true
	prevPairHead := pairNone

	for i := range key {
		c := key[i]

		if c >= 0x80 {
			return false, false
		}

		if isASCIIAlphaNum(c) {
			isUpper := c >= 'A' && c <= 'Z'
			isLower := c >= 'a' && c <= 'z'

			switch {
			case isUpper && prevIsLowerOrDigit && !prevIsUnderscore && start >= 0:
				// camelCase boundary: a lower/digit-to-upper transition closes the token.
				sensitive, pairHead := re.closeSensitiveToken(key[start:i], prevPairHead)
				if sensitive {
					return true, true
				}

				prevPairHead = pairHead
				start = i
			case isLower && prevIsUpper && start >= 0 && i-1 > start:
				// Acronym-run boundary (e.g. APIKey, JWTToken, CCNumber): the last
				// uppercase letter starts the next word, so the token ends before it.
				sensitive, pairHead := re.closeSensitiveToken(key[start:i-1], prevPairHead)
				if sensitive {
					return true, true
				}

				prevPairHead = pairHead
				start = i - 1
			case start < 0:
				start = i
			}

			prevIsUpper = isUpper
			prevIsLowerOrDigit = isLower || (c >= '0' && c <= '9')
			prevIsUnderscore = false

			continue
		}

		if start >= 0 {
			sensitive, pairHead := re.closeSensitiveToken(key[start:i], prevPairHead)
			if sensitive {
				return true, true
			}

			prevPairHead = pairHead
			start = -1
		}

		prevIsUpper = false
		prevIsLowerOrDigit = false
		prevIsUnderscore = true
	}

	if start >= 0 {
		sensitive, _ := re.closeSensitiveToken(key[start:], prevPairHead)
		if sensitive {
			return true, true
		}
	}

	return false, true
}

// closeSensitiveToken checks a completed token: it reports whether the token
// (or a two-word sensitive pair completed by it, e.g. "first"+"name",
// "national"+"id") is sensitive, and which pair head, if any, this token
// primes for the next token.
func (re *Redactor) closeSensitiveToken(tok []byte, prevPairHead int) (bool, int) {
	if re.sensitiveTokenASCII(tok) {
		return true, pairNone
	}

	if matchesPairTail(prevPairHead, tok) {
		return true, pairNone
	}

	return false, pairHeadKind(tok)
}

func isASCIIAlphaNum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// maxSensitiveTokenLen bounds the length of a key token the engine classifies.
// It must be at least the longest sensitiveTokens key ("connectionstring", 16
// bytes) and bounds the size of the config tokens accepted by [WithExtraTokens]
// / [WithoutTokens]. TestSensitiveTokensLookup guards it when new tokens are
// added. It does NOT cap the tokens the engine classifies: over-long glued keys
// still reach the root-suffix rule (see sensitiveTokenASCII).
const maxSensitiveTokenLen = 32

// sensitiveTokenASCII reports whether tok is a sensitive keyword,
// case-insensitively. Tokens up to maxSensitiveTokenLen lowercase into a fixed
// stack buffer and rely on the compiler's allocation-free map[string(bytes)]
// lookup optimization, so sensitiveTokens stays the single source of truth for
// the built-in set, adjusted by the instance's extra and dropped tokens. A
// rare over-long glued token (which only the length-agnostic root-suffix rule
// can match anyway) takes one heap lowercase so it is still classified, keeping
// the ASCII and normalized paths in agreement.
func (re *Redactor) sensitiveTokenASCII(tok []byte) bool {
	n := len(tok)
	if n == 0 {
		return false
	}

	if n > maxSensitiveTokenLen {
		return re.sensitiveToken(strings.ToLower(string(tok)))
	}

	var buf [maxSensitiveTokenLen]byte
	for i, c := range tok {
		buf[i] = lowerASCIIByte(c)
	}

	return re.sensitiveToken(string(buf[:n]))
}

// sensitiveToken checks a lowercased token against the instance's token
// configuration. It matches in three modes:
//
//   - the token as-is, against the exact sets (built-in and extra, minus
//     dropped) and the glued-compound roots;
//   - the token with a trailing digit run stripped, against the same, so a
//     numbered field like "password2"/"cvv2"/"key1" matches its base (a digit
//     rarely ends an ordinary key word), so this retry stays permissive;
//   - the token with a trailing plural "s" stripped, against the roots ONLY
//     (never the short exact tokens), so "tokens"/"apikeys"/"newpasswords"
//     redact while "keys"/"cells"/"accounts" (plurals of ordinary-word tokens)
//     stay visible; an "s" is a far more ambiguous ending than a digit.
//
// The as-is match is inlined here (the hot path, most tokens are neither
// dropped nor suffixed); the two retries are reached only for tokens that
// actually end in a digit or an "s", so their helper call is off the hot path.
//
//nolint:gocognit,gocyclo,cyclop // Deliberately flat: extra helper calls cost a frame per token.
func (re *Redactor) sensitiveToken(lowered string) bool {
	n := len(lowered)
	if n == 0 {
		return false
	}

	if re.dropTokens != nil {
		if _, dropped := re.dropTokens[lowered]; dropped {
			return false
		}
	}

	if _, ok := sensitiveTokens[lowered]; ok {
		return true
	}

	if re.extraTokens != nil {
		if _, ok := re.extraTokens[lowered]; ok {
			return true
		}
	}

	if n >= minGluedCompoundLen && re.hasSensitiveRootSuffix(lowered) {
		return true
	}

	// The digit- and plural-strip retries live behind this tail branch so they
	// are reached only for tokens that actually end in a digit or an "s".
	if last := lowered[n-1]; (last >= '0' && last <= '9') || (last == 's' && n >= 2) {
		return re.sensitiveTokenSuffix(lowered)
	}

	return false
}

// sensitiveTokenSuffix handles the two suffix-stripped retries for a token that
// ends in a digit or a plural "s"; see sensitiveToken for the rationale.
func (re *Redactor) sensitiveTokenSuffix(lowered string) bool {
	n := len(lowered)

	if lowered[n-1] == 's' {
		return re.matchesRoot(lowered[:n-1])
	}

	base := lowered
	for len(base) > 0 {
		if c := base[len(base)-1]; c < '0' || c > '9' {
			break
		}

		base = base[:len(base)-1]
	}

	return base != "" && re.matchesTokenOrRoot(base)
}

// matchesTokenOrRoot reports whether tok is a sensitive exact token (built-in or
// extra, minus dropped) or a glued compound ending in a root.
func (re *Redactor) matchesTokenOrRoot(tok string) bool {
	if re.dropTokens != nil {
		if _, dropped := re.dropTokens[tok]; dropped {
			return false
		}
	}

	if _, ok := sensitiveTokens[tok]; ok {
		return true
	}

	if re.extraTokens != nil {
		if _, ok := re.extraTokens[tok]; ok {
			return true
		}
	}

	return len(tok) >= minGluedCompoundLen && re.hasSensitiveRootSuffix(tok)
}

// matchesRoot reports whether tok is one of the strong roots (minus dropped) or
// a glued compound ending in one; the exact-token set is deliberately excluded
// (see sensitiveToken's plural mode).
func (re *Redactor) matchesRoot(tok string) bool {
	if tok == "" {
		return false
	}

	if re.dropTokens != nil {
		if _, dropped := re.dropTokens[tok]; dropped {
			return false
		}
	}

	if _, ok := sensitiveRootSet[tok]; ok {
		return true
	}

	return len(tok) >= minGluedCompoundLen && re.hasSensitiveRootSuffix(tok)
}

// hasSensitiveRootSuffix reports whether tok (at least minGluedCompoundLen
// bytes, checked by the caller) is a glued compound ending in one of the
// sensitive roots ("newpassword", "awssecretkey"). A token equal to a root is
// not matched here: that is an exact hit handled by the caller, so a root
// dropped with [WithoutTokens] stays dropped, and the drop also disables its
// suffix rule, keeping the two coherent.
func (re *Redactor) hasSensitiveRootSuffix(tok string) bool {
	for _, root := range sensitiveRootsByLastByte[tok[len(tok)-1]] {
		if len(tok) <= len(root) || !strings.HasSuffix(tok, root) {
			continue
		}

		if re.dropTokens != nil {
			if _, dropped := re.dropTokens[root]; dropped {
				continue
			}
		}

		return true
	}

	return false
}

// isStrongSecretName reports whether key names an unambiguous secret: some
// token is a strong secret token or a glued compound ending in a root. Unlike
// isSensitiveKey it ignores the weak/financial/PII tokens, so the labeled-secret
// rule redacts `{"name":"DB_PASSWORD",...}` / `{"name":"api_key",...}` without
// blanking ordinary `{"name":"cardType"|"payment"|"account",...}` data.
func (re *Redactor) isStrongSecretName(key []byte) bool {
	rest := normalizeKey(string(key))

	for {
		tok, remainder, found := strings.Cut(rest, "_")
		if tok != "" && re.isStrongSecretToken(tok) {
			return true
		}

		if !found {
			return false
		}

		rest = remainder
	}
}

// isStrongSecretToken reports whether a single normalized token is a strong
// secret token (built-in set minus dropped) or a glued compound ending in a root.
func (re *Redactor) isStrongSecretToken(tok string) bool {
	if re.dropTokens != nil {
		if _, dropped := re.dropTokens[tok]; dropped {
			return false
		}
	}

	if _, ok := strongSecretTokens[tok]; ok {
		return true
	}

	return len(tok) >= minGluedCompoundLen && re.hasSensitiveRootSuffix(tok)
}

func lowerASCIIByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}

	return c
}

func equalsASCIIFold(tok []byte, lit string) bool {
	if len(tok) != len(lit) {
		return false
	}

	for i := range tok {
		c := tok[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}

		if c != lit[i] {
			return false
		}
	}

	return true
}

//nolint:gocognit
func (re *Redactor) isSensitiveNormalizedKeyTokens(normalized string) bool {
	if normalized == "" {
		return false
	}

	rest := normalized
	prev := ""

	for {
		tok, remainder, found := strings.Cut(rest, "_")
		if tok != "" {
			if re.sensitiveToken(tok) {
				return true
			}

			if prev != "" && matchesPairString(prev, tok) {
				return true
			}

			prev = tok
		}

		if !found {
			return false
		}

		rest = remainder
	}
}
