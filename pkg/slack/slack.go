/*
Package slack provides a client for sending messages to Slack via Incoming
Webhooks.

Slack webhook reference:
https://api.slack.com/messaging/webhooks

# What It Provides

  - [New] creates a configured webhook client.
  - [Client.Send] sends a text message with per-message overrides for username,
    icon emoji, icon URL, and channel, while falling back to client defaults.
  - [Client.HealthCheck] performs an availability check against a status
    endpoint (default: Slack Status API) with a dedicated ping timeout.

# Usage

	c, err := slack.New(
	    webhookURL,
	    "my-bot",
	    ":rocket:",
	    "",
	    "#deployments",
	)
	if err != nil {
	    return err
	}

	if err := c.HealthCheck(ctx); err != nil {
	    return err
	}

	err = c.Send(ctx,
	    "deployment succeeded",
	    "", // use default username
	    "", // use default icon emoji
	    "", // use default icon URL
	    "", // use default channel
	)
	if err != nil {
	    return err
	}

# When To Use

  - A service sends alerts or deployment notices to a channel.
  - Sender name, icon, and channel have defaults but must be overridable per
    message.
*/
package slack
