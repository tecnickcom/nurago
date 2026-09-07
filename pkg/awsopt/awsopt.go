/*
Package awsopt configures the aws-sdk-go-v2 library consistently across multiple
AWS service clients. It centralizes [config.LoadOptionsFunc] calls into a
composable [Options] slice that can be built once and handed to any AWS-based
package in this library.

# How It Works

[Options] is a typed slice of [config.LoadOptionsFunc] values. Options are
accumulated with method calls and then materialized into an [aws.Config] via
[Options.LoadDefaultConfig], which delegates to the standard
[config.LoadDefaultConfig] from the SDK:

 1. Create an [Options] value (zero value is ready to use).
 2. Append options with [Options.WithAWSOption], [Options.WithRegion], or
    [Options.WithRegionFromURL].
 3. Call [Options.LoadDefaultConfig] to obtain an [aws.Config] that can be
    passed directly to any aws-sdk-go-v2 service constructor.

# Integrated Packages

awsopt is used as the AWS configuration layer by:
  - [github.com/tecnickcom/nurago/pkg/awssecretcache]
  - [github.com/tecnickcom/nurago/pkg/s3]
  - [github.com/tecnickcom/nurago/pkg/sqs]

# Usage

	var opts awsopt.Options
	opts.WithRegionFromURL("https://s3.eu-west-1.amazonaws.com", "")

	awsCfg, err := opts.LoadDefaultConfig(ctx)
	if err != nil {
	    return err
	}

	// awsCfg is ready for any aws-sdk-go-v2 service client:
	// s3.NewFromConfig(awsCfg), secretsmanager.NewFromConfig(awsCfg), …

When consuming an awsopt-based package, pass additional options via the
package's own WithAWSOptions helper so all AWS clients in the process share
consistent configuration.

# When To Use

  - Several AWS clients in one service must use the same region, credentials,
    and endpoint settings.
  - The region is derived from an endpoint URL supplied by configuration.
*/
package awsopt

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

const (
	// awsRegionShapeRegexp matches an AWS region code (e.g. us-east-1, eu-west-2,
	// ap-southeast-1, us-gov-west-1, us-iso-east-1, cn-north-1). It is the single
	// invariant region extraction relies on: the geo prefix, hyphen-separated
	// words, and a trailing number are stable across every AWS partition, whereas
	// the DNS suffix (amazonaws.com, api.aws, amazonaws.com.cn, c2s.ic.gov, …) is
	// not. Matching on the shape keeps the parser partition-agnostic and rejects
	// non-region labels such as service names ("s3") or the "vpce" label in VPC
	// endpoint DNS.
	awsRegionShapeRegexp = `^[a-z]{2}(?:-[a-z]+)+-\d+$`
)

// awsRegionShapeRe is the precompiled region-shape validator (compiled once at package load).
var awsRegionShapeRe = regexp.MustCompile(awsRegionShapeRegexp)

// Options is a set of all AWS options to apply.
//
// An Options value is not safe for concurrent modification: build it from a
// single goroutine with the With* methods, then share the finished value
// read-only (for example by passing it to a consuming package's WithAWSOptions).
type Options []config.LoadOptionsFunc

// LoadDefaultConfig builds an aws.Config from the accumulated options.
//
// It turns one shared Options value into an SDK configuration in a single call,
// delegating to the SDK's [config.LoadDefaultConfig]. This keeps region,
// credentials, and any custom SDK loaders consistent across packages that
// consume awsopt.
func (c *Options) LoadDefaultConfig(ctx context.Context) (aws.Config, error) {
	o := make([]func(*config.LoadOptions) error, len(*c))
	for k, v := range *c {
		o[k] = v
	}

	return config.LoadDefaultConfig(ctx, o...) //nolint:wrapcheck
}

// WithAWSOption appends any raw aws-sdk-go-v2 load option.
//
// Use it as the escape hatch for an SDK feature that has no dedicated helper in
// this package.
func (c *Options) WithAWSOption(opt config.LoadOptionsFunc) {
	*c = append(*c, opt)
}

// WithRegion appends an explicit AWS region option.
//
// Use it when the region is already known. Every downstream AWS client built
// from the same Options value uses this region.
func (c *Options) WithRegion(region string) {
	c.WithAWSOption(config.WithRegion(region))
}

// WithRegionFromURL appends a region derived from an AWS endpoint URL.
//
// The region is the right-most host label that looks like an AWS region code.
// This is partition-agnostic (it does not depend on the DNS suffix), so every
// endpoint form works, including future partitions, with no code changes:
//
//	https://sqs.eu-west-1.amazonaws.com                -> eu-west-1     (standard)
//	https://ec2.us-west-2.api.aws                      -> us-west-2     (dual-stack)
//	https://sqs.cn-north-1.amazonaws.com.cn            -> cn-north-1    (China)
//	https://sqs.us-iso-east-1.c2s.ic.gov               -> us-iso-east-1 (ISO)
//	https://vpce-0abc.sqs.us-east-1.vpce.amazonaws.com -> us-east-1     (VPC endpoint)
//
// The scheme is optional, matching is case-insensitive, and userinfo, port, and
// path are ignored (only the host is inspected). Scanning right-to-left means a
// region-shaped label further left (such as a bucket named like a region in
// "bucket.s3.eu-west-1.amazonaws.com") does not shadow the real region.
//
// The region must be its own dot-separated host label. Endpoints that fold the
// region into a larger label are not recognized and fall back, notably the
// dash-style S3 website endpoint "bucket.s3-website-us-east-1.amazonaws.com"
// (the dot-style "bucket.s3-website.us-east-1.amazonaws.com" works).
//
// Because it is partition-agnostic, the host is not verified to be AWS-owned, so
// a non-AWS URL that happens to carry a region-shaped label yields that region.
// Pass a genuine AWS endpoint.
//
// When the URL carries no region, defaultRegion is used if it is non-empty.
// Otherwise no region option is added at all: region resolution is left to the
// SDK's [config.LoadDefaultConfig], which applies AWS's canonical precedence
// (AWS_REGION, then AWS_DEFAULT_REGION, then the shared config file
// ~/.aws/config, then EC2 IMDS). It only pins a region when it has an explicit
// one (from the URL or defaultRegion) and otherwise defers to the SDK rather
// than substituting a hardcoded default.
func (c *Options) WithRegionFromURL(rawURL, defaultRegion string) {
	if region := awsRegionFromURL(rawURL, defaultRegion); region != "" {
		c.WithRegion(region)
	}
}

// awsRegionFromURL returns the region encoded in a service endpoint URL, falling
// back to defaultRegion.
//
// The URL is parsed case-insensitively and the right-most region-shaped host
// label is used (see regionFromHost). It returns "" when the URL has no region
// and defaultRegion is empty, signaling the caller to leave region resolution to
// the SDK.
func awsRegionFromURL(rawURL, defaultRegion string) string {
	if region := regionFromHost(rawURL); region != "" {
		return region
	}

	return defaultRegion
}

// regionFromHost returns the AWS region encoded in an endpoint URL, or "" when no
// host label looks like a region.
//
// It scans the host labels right-to-left and returns the right-most one matching
// the AWS region shape. This is deliberately partition-agnostic: it never inspects
// the DNS suffix, so it works across every AWS endpoint form and partition
// ("service.<region>.amazonaws.com", dual-stack "service.<region>.api.aws", China
// "...amazonaws.com.cn", GovCloud, ISO "...c2s.ic.gov", VPC endpoints
// "<id>.<service>.<region>.vpce.amazonaws.com" where "vpce" is skipped, and any
// future partition) with no code changes. Scanning right-to-left picks the region
// closest to the domain, so region-shaped labels further left (e.g. a bucket named
// like a region) do not shadow it.
//
// Because the host is not verified to be AWS-owned, a non-AWS URL that happens to
// contain a region-shaped label yields that label; the caller is expected to pass
// a genuine AWS endpoint. Only the URL host is examined (never the path), as the
// input is parsed so scheme, userinfo, port, and path are ignored.
func regionFromHost(rawURL string) string {
	labels := strings.Split(hostFromURL(rawURL), ".")

	for _, label := range slices.Backward(labels) {
		if awsRegionShapeRe.MatchString(label) {
			return label
		}
	}

	return ""
}

// hostFromURL returns the lower-cased host of a URL, without any userinfo or
// port. The scheme is optional: when absent, the input is treated as a network
// authority so scheme-less endpoints (e.g. localstack "sqs.eu-west-1.amazonaws.com")
// parse correctly. It returns "" for blank or unparseable input.
func hostFromURL(rawURL string) string {
	raw := strings.ToLower(strings.TrimSpace(rawURL))
	if raw == "" {
		return ""
	}

	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	if u.Host == "" {
		// Scheme-less input (e.g. "host/path"): url.Parse put it in Path/Opaque.
		// Reparse as a network authority so the host is recognized. Keying off the
		// parsed result (not a "://" substring) also handles a scheme-less URL
		// whose path or query legitimately contains "://".
		u, err = url.Parse("//" + raw)
		if err != nil {
			return ""
		}
	}

	return u.Hostname()
}
