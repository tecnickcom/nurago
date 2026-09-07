package kafka_test

import (
	"context"
	"errors"
	"fmt"

	libkafka "github.com/segmentio/kafka-go"
	"github.com/tecnickcom/nurago/pkg/kafka"
)

// exampleQueue is a minimal in-memory stand-in for a Kafka topic, satisfying
// both the kafka.KWriter and kafka.KReader interfaces. Real code omits
// WithKafkaWriter and WithKafkaReader and lets the constructors dial the
// brokers.
type exampleQueue struct {
	messages []libkafka.Message
}

func (q *exampleQueue) WriteMessages(_ context.Context, msgs ...libkafka.Message) error {
	q.messages = append(q.messages, msgs...)

	return nil
}

func (q *exampleQueue) ReadMessage(_ context.Context) (libkafka.Message, error) {
	if len(q.messages) == 0 {
		return libkafka.Message{}, errors.New("queue is empty")
	}

	msg := q.messages[0]
	q.messages = q.messages[1:]

	return msg, nil
}

func (q *exampleQueue) FetchMessage(ctx context.Context) (libkafka.Message, error) {
	return q.ReadMessage(ctx)
}

func (q *exampleQueue) CommitMessages(_ context.Context, _ ...libkafka.Message) error {
	return nil
}

func (q *exampleQueue) Close() error { return nil }

func ExampleProducer_Send() {
	queue := &exampleQueue{}

	producer, err := kafka.NewProducer(
		[]string{"127.0.0.1:9092"},
		"events",
		kafka.WithKafkaWriter(queue),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = producer.Close() }()

	ctx := context.TODO()

	err = producer.Send(ctx, []byte("raw payload"))
	if err != nil {
		fmt.Println(err)

		return
	}

	consumer, err := kafka.NewConsumer(
		[]string{"127.0.0.1:9092"},
		"events",
		"example-group",
		kafka.WithKafkaReader(queue),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = consumer.Close() }()

	msg, err := consumer.Receive(ctx)

	fmt.Println(string(msg), err)

	// Output:
	// raw payload <nil>
}

func ExampleProducer_SendData() {
	type event struct {
		Type string `json:"type"`
		User string `json:"user"`
	}

	queue := &exampleQueue{}

	producer, err := kafka.NewProducer(
		[]string{"127.0.0.1:9092"},
		"events",
		kafka.WithKafkaWriter(queue),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = producer.Close() }()

	ctx := context.TODO()

	// SendData and ReceiveData serialize the value, so structs round-trip
	// without the caller writing encoding logic. The codec is replaceable
	// through WithMessageEncodeFunc and WithMessageDecodeFunc.
	err = producer.SendData(ctx, event{Type: "login", User: "alice"})
	if err != nil {
		fmt.Println(err)

		return
	}

	consumer, err := kafka.NewConsumer(
		[]string{"127.0.0.1:9092"},
		"events",
		"example-group",
		kafka.WithKafkaReader(queue),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = consumer.Close() }()

	var got event

	err = consumer.ReceiveData(ctx, &got)

	fmt.Printf("%+v %v\n", got, err)

	// Output:
	// {Type:login User:alice} <nil>
}

func ExampleConsumer_FetchMessage() {
	queue := &exampleQueue{}

	producer, err := kafka.NewProducer(
		[]string{"127.0.0.1:9092"},
		"events",
		kafka.WithKafkaWriter(queue),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = producer.Close() }()

	ctx := context.TODO()

	err = producer.Send(ctx, []byte("at-least-once payload"))
	if err != nil {
		fmt.Println(err)

		return
	}

	consumer, err := kafka.NewConsumer(
		[]string{"127.0.0.1:9092"},
		"events",
		"example-group",
		kafka.WithKafkaReader(queue),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = consumer.Close() }()

	// FetchMessage leaves the offset uncommitted, so the message is
	// redelivered if the process dies before CommitMessages succeeds.
	msg, err := consumer.FetchMessage(ctx)
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Println(string(msg.Value))

	fmt.Println(consumer.CommitMessages(ctx, msg))

	// Output:
	// at-least-once payload
	// <nil>
}
