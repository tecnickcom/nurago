/*
Package s3 uploads, downloads, lists, and deletes S3 bucket objects through the
AWS SDK v2 S3 client.

It covers the common object operations:

  - upload object data,
  - download object data,
  - list object keys by prefix,
  - delete objects.

It is built on github.com/aws/aws-sdk-go-v2/service/s3.

# What It Provides

  - [New] to create a bucket-scoped [Client].
  - [Client.Put] to upload from an [io.Reader].
  - [Client.Get] to fetch an object and access its body stream.
  - [Client.ListKeys] to list object keys, optionally filtered by prefix.
  - [Client.ListObjects] to list objects with per-object metadata (size,
    last-modified, ETag), optionally filtered by prefix.
  - [Client.Delete] to remove an object by key.
  - [Client.HealthCheck] to verify bucket reachability and access permissions.

# Configuration & Extensibility

The client configuration composes with [github.com/tecnickcom/nurago/pkg/awsopt]
and exposes option hooks:

  - [WithAWSOptions] to pass generic AWS config options,
  - [WithSrvOptionFuncs] to customize S3 service options,
  - [WithS3Client] to inject a custom S3 implementation (tests and advanced
    integrations; skips AWS configuration loading),
  - [WithEndpointMutable] and [WithEndpointImmutable] for endpoint overrides
    (useful for local S3-compatible environments and tests).

# Usage

	c, err := s3.New(ctx, "my-bucket")
	if err != nil {
	    return err
	}

	if err := c.Put(ctx, "reports/latest.json", reader); err != nil {
	    return err
	}

	obj, err := c.Get(ctx, "reports/latest.json")
	if err != nil {
	    return err
	}
	_ = obj

	keys, err := c.ListKeys(ctx, "reports/")
	if err != nil {
	    return err
	}
	_ = keys

	if err := c.Delete(ctx, "reports/old.json"); err != nil {
	    return err
	}

	if err := c.HealthCheck(ctx); err != nil {
	    return err
	}

# When To Use

  - A service puts, gets, lists, and deletes objects and nothing more exotic.
  - S3 should be reported on the service health endpoint.
*/
package s3
