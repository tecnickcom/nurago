package valkey_test

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tecnickcom/nurago/pkg/valkey"
	libvalkey "github.com/valkey-io/valkey-go"
	"github.com/valkey-io/valkey-go/mock"
	"go.uber.org/mock/gomock"
)

// exampleReporter satisfies gomock.TestReporter outside a test function, so
// the examples can drive the generated valkey mock. Test code passes *testing.T
// instead.
type exampleReporter struct{}

func (exampleReporter) Errorf(format string, args ...any) {
	_, _ = os.Stdout.WriteString(fmt.Sprintf(format, args...) + "\n")
}

func (exampleReporter) Fatalf(format string, args ...any) {
	_, _ = os.Stdout.WriteString(fmt.Sprintf(format, args...) + "\n")
}

func ExampleClient_Set() {
	ctrl := gomock.NewController(exampleReporter{})
	defer ctrl.Finish()

	ctx := context.TODO()
	vkc := mock.NewClient(ctrl)

	// The client speaks the Valkey wire protocol, so a TTL becomes an EX
	// argument on the SET command.
	vkc.EXPECT().Do(ctx, mock.Match("SET", "greeting", "hello", "EX", "60"))
	vkc.EXPECT().Do(ctx, mock.Match("GET", "greeting")).
		Return(mock.Result(mock.ValkeyString("hello")))
	vkc.EXPECT().Close()

	// Real code omits WithValkeyClient and lets New dial the server
	// described by SrvOptions.
	client, err := valkey.New(
		ctx,
		libvalkey.ClientOption{InitAddress: []string{"127.0.0.1:6379"}},
		valkey.WithValkeyClient(vkc),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer client.Close()

	err = client.Set(ctx, "greeting", "hello", time.Minute)
	if err != nil {
		fmt.Println(err)

		return
	}

	value, err := client.Get(ctx, "greeting")

	fmt.Println(value, err)

	// Output:
	// hello <nil>
}

func ExampleClient_HealthCheck() {
	ctrl := gomock.NewController(exampleReporter{})
	defer ctrl.Finish()

	ctx := context.TODO()
	vkc := mock.NewClient(ctrl)

	vkc.EXPECT().Do(ctx, mock.Match("PING")).
		Return(mock.Result(mock.ValkeyString("PONG")))
	vkc.EXPECT().Close()

	client, err := valkey.New(
		ctx,
		libvalkey.ClientOption{InitAddress: []string{"127.0.0.1:6379"}},
		valkey.WithValkeyClient(vkc),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer client.Close()

	// HealthCheck satisfies healthcheck.HealthChecker, so Valkey can be
	// registered directly on a service health endpoint.
	fmt.Println(client.HealthCheck(ctx))

	// Output:
	// <nil>
}

func ExampleMessageEncode() {
	type payload struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	// The default encoder serializes to a transport-safe string, which the
	// matching decoder reverses.
	msg, err := valkey.MessageEncode(payload{ID: 7, Name: "alice"})
	if err != nil {
		fmt.Println(err)

		return
	}

	var out payload

	err = valkey.MessageDecode(msg, &out)

	fmt.Printf("%+v %v\n", out, err)

	// Output:
	// {ID:7 Name:alice} <nil>
}
