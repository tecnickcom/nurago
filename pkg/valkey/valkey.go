/*
Package valkey wraps the valkey-go client
(https://github.com/valkey-io/valkey-go) for Valkey (https://valkey.io), a
Redis-compatible in-memory data store. It covers key/value storage, typed data
serialization, and Pub/Sub messaging behind a single [Client] type.

# How It Works

[New] creates a [Client] given a [SrvOptions] (aliased from
valkey-go's ClientOption) and a variadic list of [Option] values:

 1. The server address is validated before any network connection is attempted
    (skipped when a client is injected via [WithValkeyClient], since no
    connection is dialed).
 2. A valkey-go client is constructed (or injected via [WithValkeyClient] for
    tests). When at least one channel is declared with [WithChannels], a
    pre-built, pinned Pub/Sub subscription command starts a background
    subscription that feeds [Client.Receive] and [Client.ReceiveData]
    one message per call. The subscription runs until [Client.Close] is
    called: canceling the [New] context does not stop it.
 3. Encode and decode functions (defaulting to [DefaultMessageEncodeFunc] and
    [DefaultMessageDecodeFunc]) are stored on the client and used
    transparently by the typed data methods.

# Operations

  - [Client.Set], [Client.Get], and [Client.Del] provide raw key/value access
    with expiration.
  - [Client.SetData] and [Client.GetData] encode and decode Go values with the
    configured [TEncodeFunc] and [TDecodeFunc].
  - [Client.Send] and [Client.Receive] carry raw strings; [Client.SendData] and
    [Client.ReceiveData] apply the same codec and return the channel name with
    the decoded value.
  - [WithMessageEncodeFunc] and [WithMessageDecodeFunc] replace the default
    JSON+base64 codec.
  - [Client.HealthCheck] sends a PING and returns a wrapped error on failure.
  - A missing key surfaces as [ErrKeyNotFound]; other configuration and
    subscription states surface as the exported Err values, all matchable with
    errors.Is.
  - [WithValkeyClient] injects a custom [VKClient] for testing.
  - [Client.Close] drains pending calls before releasing the connection, and is
    required to stop the background subscription when channels are configured.

# Usage

	srvOpts := valkey.SrvOptions{InitAddress: []string{"localhost:6379"}}

	client, err := valkey.New(
	    ctx,
	    srvOpts,
	    valkey.WithChannels("events", "notifications"),
	)
	if err != nil {
	    return err
	}
	defer client.Close()

	// Store and retrieve a typed value:
	type Payload struct{ Message string }

	if err := client.SetData(ctx, "my-key", Payload{"hello"}, time.Hour); err != nil {
	    return err
	}

	var p Payload
	if err := client.GetData(ctx, "my-key", &p); err != nil {
	    return err
	}

	// Publish and consume a typed message:
	if err := client.SendData(ctx, "events", Payload{"fired"}); err != nil {
	    return err
	}

	var event Payload
	channel, err := client.ReceiveData(ctx, &event)

To swap in an encrypted codec, supply custom functions at construction time:

	client, err := valkey.New(ctx, srvOpts,
	    valkey.WithMessageEncodeFunc(myEncryptAndEncode),
	    valkey.WithMessageDecodeFunc(myDecryptAndDecode),
	)

# When To Use

  - You run Valkey rather than Redis and want the same shape of API as
    [github.com/tecnickcom/nurago/pkg/redis].
  - Structs should round-trip without hand-written encoding at each call
    site.
*/
package valkey
