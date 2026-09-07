/*
Package jirasrv provides a typed HTTP client foundation for Jira Server REST
integrations.

It wraps low-level HTTP plumbing in a [Client] and exposes a generic request
entry point ([Client.SendRequest]) along with typed Jira data models for common
payloads (issues, transitions, comments, users, visibility, properties,
metadata).

Core responsibilities handled by the client:
  - API base-path composition (`/rest/api/2`)
  - bearer-token authentication headers
  - JSON request encoding and content headers
  - request payload validation
  - method-aware HTTP retry behavior
  - connection and service health probing via [Client.HealthCheck]

The client supports dependency injection and runtime tuning via options:
  - custom HTTP transport/client ([WithHTTPClient])
  - request and ping timeouts ([WithTimeout], [WithPingTimeout])
  - retry policy controls ([WithRetryAttempts], [WithRetryDelay])

# Reference

Jira Server REST API reference:
  - https://docs.atlassian.com/software/jira/docs/api/REST/9.17.0/

# When To Use

  - A service reads or updates Jira issues.
  - You want authentication, retries, and encoding handled, while choosing
    the endpoint yourself.
*/
package jirasrv
