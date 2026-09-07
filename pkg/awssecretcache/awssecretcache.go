/*
Package awssecretcache provides a local, thread-safe, fixed-size cache for AWS
Secrets Manager lookups, with single-flight deduplication.

[New] wraps an aws-sdk-go-v2 SecretsManager client
(https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/secretsmanager) in a
[github.com/tecnickcom/nurago/pkg/sfcache] cache. Concurrent callers asking for
the same secret share a single upstream GetSecretValue call; the result is
cached for the TTL.

# Usage

	cache, err := awssecretcache.New(
	    ctx,
	    128,             // maximum number of cached secrets
	    5*time.Minute,   // TTL per entry
	    awssecretcache.WithEndpointMutable("https://secretsmanager.us-east-1.amazonaws.com"),
	)
	if err != nil {
	    return err
	}

	value, err := cache.GetSecretString(ctx, "prod/myapp/db-password")
	if err != nil {
	    return err
	}

After a secret rotation, evict the entry immediately:

	cache.Remove("prod/myapp/db-password")

# Caching

Only successful lookups are cached, for the TTL. Errors are shared with the
callers coalesced onto the same lookup but never cached, so the next call
retries.

Expired entries are not removed at the TTL: they are replaced by the next lookup
or evicted under capacity pressure, so **expired secret material can remain in
process memory until then**. Use [Cache.Remove], [Cache.Reset] or
[Cache.PurgeExpired] when prompt removal matters.

The size given to [New] bounds the number of SECRETS held, never the number of
distinct secrets requested. [Cache.Len] can exceed it: it also counts the
secrets being fetched and the residue of a failed fetch, and a stale serve that
can evict nothing holds one secret more.

When the cache is full, storing a secret evicts an expired entry holding nothing
worth keeping first, then a secret only being served stale, and only then the
entry closest to expiration (expiry-ordered, not LRU: reads do not refresh
recency). A fetch that FAILS evicts nothing of value.

# Stale-if-error

[WithStaleOnFailure] serves the last known good secret for a window measured
from the first failed refresh, so it protects rarely read secrets too.
[WithStaleIfError] is the RFC 5861 variant, whose window is anchored to the
secret's original expiration and therefore only covers secrets read more often
than ttl + maxStale.

With either enabled, a failed refresh returns the last known good secret with a
NIL error: callers cannot tell a stale secret from a fresh one, and a secret
rotated upstream during an outage keeps being served until the window closes.

# Retrieval

[Cache.GetSecretData] returns the raw SDK output, shared by reference: treat it
as read-only. [Cache.GetSecretString] and [Cache.GetSecretBinary] return a copy
and handle both storage formats (SecretString and SecretBinary).

# AWS configuration

[WithAWSOptions], [WithSrvOptionFuncs], [WithEndpointMutable] and
[WithEndpointImmutable] customize the SDK client. [WithSecretsManagerClient]
injects a client directly, in which case the AWS configuration is neither loaded
nor used.

# When To Use

  - A secret is read on a request path and Secrets Manager charges per API
    call.
  - Rotation happens on a known schedule, so a bounded TTL is acceptable.
*/
package awssecretcache
