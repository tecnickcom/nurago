package awsopt_test

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/tecnickcom/nurago/pkg/awsopt"
)

func ExampleOptions() {
	// The zero value is ready to use.
	var opts awsopt.Options

	opts.WithRegion("eu-west-1")

	// Any config.LoadOptionsFunc from the SDK can be appended, so nothing in
	// the SDK is out of reach. Static credentials keep the example offline;
	// real code relies on the default credential chain.
	opts.WithAWSOption(config.WithCredentialsProvider(
		credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
	))

	awsCfg, err := opts.LoadDefaultConfig(context.TODO())
	if err != nil {
		fmt.Println(err)

		return
	}

	// awsCfg is passed directly to any aws-sdk-go-v2 service constructor,
	// for example s3.NewFromConfig or secretsmanager.NewFromConfig.
	fmt.Println(awsCfg.Region)

	// Output:
	// eu-west-1
}

func ExampleOptions_WithRegionFromURL() {
	var opts awsopt.Options

	// The region is extracted from a service endpoint, which suits
	// deployments configured with a single endpoint URL. The second
	// argument is the fallback when no region can be parsed.
	opts.WithRegionFromURL("https://s3.eu-central-1.amazonaws.com", "us-east-1")
	opts.WithAWSOption(config.WithCredentialsProvider(
		credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
	))

	awsCfg, err := opts.LoadDefaultConfig(context.TODO())
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Println(awsCfg.Region)

	// Output:
	// eu-central-1
}
