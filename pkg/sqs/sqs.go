/*
Package sqs wraps github.com/aws/aws-sdk-go-v2/service/sqs with an API that
covers the common queue workflow: send, receive, decode, acknowledge (delete),
and health-check.

# How It Works

[New] creates a [Client] bound to a queue URL and optional message-group ID.

  - queueURL must be a valid absolute URL. For FIFO queues (URL ends with
    `.fifo`), a valid message-group ID is required; for standard queues it must
    be empty, and a non-empty value is rejected with [ErrUnexpectedMessageGroupID].
    Argument validation runs before any AWS configuration is loaded.
  - The AWS region is derived from the queue URL when it is not set explicitly,
    so callers rarely need to repeat it; an explicit region supplied via
    [WithAWSOptions] still takes precedence.
  - For FIFO queues, [Client.Send] and [Client.SendData] do not set a
    MessageDeduplicationId, so the queue must have ContentBasedDeduplication
    enabled; otherwise supply an explicit per-message deduplication ID via
    [Client.SendWithDeduplicationID] or [Client.SendDataWithDeduplicationID].
  - Long polling and visibility are configurable via [WithWaitTimeSeconds] and
    [WithVisibilityTimeout], with defaults of [DefaultWaitTimeSeconds] (20s)
    and [DefaultVisibilityTimeout] (600s).
  - Payloads can be sent/received as raw strings ([Client.Send],
    [Client.Receive]) or typed data ([Client.SendData], [Client.ReceiveData])
    through pluggable encode/decode hooks.
  - Configuration and argument problems are reported as exported sentinel
    errors (see [ErrInvalidQueueURL] and the others in this package) that
    callers can match with errors.Is.

Receive flow behavior:

  - [Client.Receive] returns nil,nil when no message arrives within the
    long-poll window.
  - [Client.ReceiveData] returns an empty receipt handle when no message is
    available.
  - After successful processing, callers should acknowledge via
    [Client.Delete] using the receipt handle.
  - If decode fails, [Client.ReceiveData] still returns the receipt handle so
    callers can choose whether to delete or re-queue according to their policy.

# Usage

	c, err := sqs.New(ctx,
	    "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue",
	    "", // non-FIFO queue
	    sqs.WithWaitTimeSeconds(20),
	    sqs.WithVisibilityTimeout(300),
	)
	if err != nil {
	    return err
	}

	// Send typed payload
	if err := c.SendData(ctx, event); err != nil {
	    return err
	}

	// Receive typed payload
	var msg Event
	receiptHandle, err := c.ReceiveData(ctx, &msg)
	if err != nil {
	    return err
	}
	if receiptHandle != "" {
	    _ = c.Delete(ctx, receiptHandle)
	}

# When To Use

  - A worker consumes a queue and acknowledges messages after processing.
  - Message bodies are structs and should be encoded and decoded
    consistently.
*/
package sqs
