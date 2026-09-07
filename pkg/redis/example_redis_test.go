package redis_test

import (
	"context"
	"fmt"
	"time"

	libredis "github.com/redis/go-redis/v9"
	"github.com/tecnickcom/nurago/pkg/redis"
)

// exampleRClient is a minimal in-memory stand-in for the go-redis client,
// implementing only the redis.RClient interface. Real code omits
// WithRedisClient and lets New dial the server described by SrvOptions.
type exampleRClient struct {
	data map[string]string
}

func (c *exampleRClient) Close() error { return nil }

func (c *exampleRClient) Del(_ context.Context, keys ...string) *libredis.IntCmd {
	var n int64

	for _, k := range keys {
		if _, ok := c.data[k]; ok {
			delete(c.data, k)

			n++
		}
	}

	return libredis.NewIntResult(n, nil)
}

func (c *exampleRClient) Get(_ context.Context, key string) *libredis.StringCmd {
	v, ok := c.data[key]
	if !ok {
		return libredis.NewStringResult("", libredis.Nil)
	}

	return libredis.NewStringResult(v, nil)
}

func (c *exampleRClient) Ping(_ context.Context) *libredis.StatusCmd {
	return libredis.NewStatusResult("PONG", nil)
}

func (c *exampleRClient) Publish(_ context.Context, _ string, _ any) *libredis.IntCmd {
	return libredis.NewIntResult(1, nil)
}

func (c *exampleRClient) Set(_ context.Context, key string, value any, _ time.Duration) *libredis.StatusCmd {
	c.data[key] = fmt.Sprint(value)

	return libredis.NewStatusResult("OK", nil)
}

func (c *exampleRClient) Subscribe(_ context.Context, _ ...string) *libredis.PubSub {
	return nil
}

func ExampleClient_Set() {
	client, err := redis.New(
		context.TODO(),
		nil,
		redis.WithRedisClient(&exampleRClient{data: make(map[string]string)}),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = client.Close() }()

	ctx := context.TODO()

	err = client.Set(ctx, "greeting", "hello", time.Minute)
	if err != nil {
		fmt.Println(err)

		return
	}

	var value string

	err = client.Get(ctx, "greeting", &value)

	fmt.Println(value, err)

	fmt.Println(client.Del(ctx, "greeting"))

	// Output:
	// hello <nil>
	// <nil>
}

func ExampleClient_SetData() {
	type session struct {
		User  string `json:"user"`
		Admin bool   `json:"admin"`
	}

	client, err := redis.New(
		context.TODO(),
		nil,
		redis.WithRedisClient(&exampleRClient{data: make(map[string]string)}),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = client.Close() }()

	ctx := context.TODO()

	// SetData and GetData serialize the value, so structs round-trip
	// without the caller writing encoding logic.
	err = client.SetData(ctx, "session:7", session{User: "alice", Admin: true}, time.Hour)
	if err != nil {
		fmt.Println(err)

		return
	}

	var out session

	err = client.GetData(ctx, "session:7", &out)

	fmt.Printf("%+v %v\n", out, err)

	// Output:
	// {User:alice Admin:true} <nil>
}

func ExampleClient_HealthCheck() {
	client, err := redis.New(
		context.TODO(),
		nil,
		redis.WithRedisClient(&exampleRClient{data: make(map[string]string)}),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = client.Close() }()

	// HealthCheck satisfies healthcheck.HealthChecker, so Redis can be
	// registered directly on a service health endpoint.
	fmt.Println(client.HealthCheck(context.TODO()))

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
	msg, err := redis.MessageEncode(payload{ID: 7, Name: "alice"})
	if err != nil {
		fmt.Println(err)

		return
	}

	var out payload

	err = redis.MessageDecode(msg, &out)

	fmt.Printf("%+v %v\n", out, err)

	// Output:
	// {ID:7 Name:alice} <nil>
}
