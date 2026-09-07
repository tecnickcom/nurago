package redact_test

import (
	"fmt"

	"github.com/tecnickcom/nurago/pkg/redact"
)

const (
	testData = `
GET /v1/version HTTP/1.1
Host: test.redact.invalid
User-Agent: Go-http-client/1.1
Authorization: Basic SECRET_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789=
authorization : ApiKey=SECRET OtherData=SECRET
X-NURAGO-Trace-Id: abcdef0123456789
Accept-Encoding: gzip

password=SECRET
test_password=SECRET
PASSWORD=SECRET
TEST_PASSWORD=SECRET
key=SECRET
test_key=SECRET
KEY=SECRET
TEST_KEY=SECRET
password=SECRET&key=SECRET
alpha=beta&password=SECRET&key=SECRET&gamma=delta
Token=SECRET

{
	"password": "SECRET",
	"test_password": "SECRET",
	"PASSWORD": "SECRET",
	"TEST_PASSWORD": "SECRET",
	"key": "SECRET",
	"test_key": "SECRET",
	"KEY": "SECRET",
	"TEST_KEY": "SECRET",
	"Token": "SECRET",
	"Visa" : "4012888888881881",
	"MasterCard" : "5555555555554444",
	"American Express" : "371449635398431",
	"Diners Club" : "38520000023237",
	"Discover" : "6011000990139424",
	"JCB" : "3566002020360505"
}
`
)

func ExampleDefault() {
	// Default is the shared, zero-configuration Redactor: it covers headers,
	// secret-looking keys in JSON and URL-encoded pairs, and card numbers.
	redactedData := redact.Default().String(testData)

	fmt.Println(redactedData)

	// Output:
	// GET /v1/version HTTP/1.1
	// Host: test.redact.invalid
	// User-Agent: Go-http-client/1.1
	// Authorization: ***
	// authorization : ***
	// X-NURAGO-Trace-Id: abcdef0123456789
	// Accept-Encoding: gzip
	//
	// password=***
	// test_password=***
	// PASSWORD=***
	// TEST_PASSWORD=***
	// key=***
	// test_key=***
	// KEY=***
	// TEST_KEY=***
	// password=***&key=***
	// alpha=beta&password=***&key=***&gamma=delta
	// Token=***
	//
	// {
	// 	"password": "***",
	// 	"test_password": "***",
	// 	"PASSWORD": "***",
	// 	"TEST_PASSWORD": "***",
	// 	"key": "***",
	// 	"test_key": "***",
	// 	"KEY": "***",
	// 	"TEST_KEY": "***",
	// 	"Token": "***",
	// 	"Visa" : "***",
	// 	"MasterCard" : "***",
	// 	"American Express" : "***",
	// 	"Diners Club" : "***",
	// 	"Discover" : "***",
	// 	"JCB" : "***"
	// }
}

func ExampleNew() {
	// A custom Redactor instance: different marker, an extra sensitive key
	// token, and financial fields kept readable.
	re := redact.New(
		redact.WithMarker("#REDACTED#"),
		redact.WithExtraTokens("floof"),
		redact.WithoutTokens("amount"),
	)

	fmt.Println(re.String(`{"floof":"secret","amount":42,"password":"x"}`))

	// Output:
	// {"floof":"#REDACTED#","amount":42,"password":"#REDACTED#"}
}

func ExampleRedactor_String() {
	// String is the canonical string-in/string-out entry point.
	fmt.Println(redact.Default().String("X-Api-Key: SECRET\npassword=SECRET&note=ok"))

	// Output:
	// X-Api-Key: ***
	// password=***&note=ok
}

func ExampleRedactor_BytesToString() {
	// BytesToString is the preferred form when the caller already holds a
	// []byte, for example a dump from httputil.DumpRequest, and needs a
	// string for the log record. Its method value satisfies the RedactFn
	// option of httpclient, httpserver, and httpreverseproxy.
	dump := []byte("POST /login HTTP/1.1\nAuthorization: Bearer SECRET\n\npassword=SECRET")

	fmt.Println(redact.Default().BytesToString(dump))

	// Output:
	// POST /login HTTP/1.1
	// Authorization: ***
	//
	// password=***
}

func ExampleRedactor_AppendTo() {
	inputs := [][]byte{
		[]byte("token=SECRET&reference=VISIBLE"),
		[]byte(`{"card":"4012 8888 8888 1881"}`),
	}

	// AppendTo reuses the destination buffer across calls to avoid
	// per-call allocations on high-throughput logging paths.
	re := redact.Default()

	var dst []byte

	for _, in := range inputs {
		dst = re.AppendTo(dst, in)
		fmt.Println(string(dst))
	}

	// Output:
	// token=***&reference=VISIBLE
	// {"card":"***"}
}
