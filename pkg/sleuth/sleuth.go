/*
Package sleuth provides a Go client for the Sleuth.io API, covering common
write-side integrations for delivery metrics and operational signal ingestion.

This package supports four Sleuth workflows:

  - deployment registration,
  - manual change registration,
  - custom incident impact registration,
  - custom metric impact registration.

Sleuth API reference:
https://help.sleuth.io/sleuth-api

# How It Works

[New] initializes a [Client] with base URL, org slug, and API key, then builds
the endpoint URL templates used by each API action.

For each send operation:

 1. The request struct is validated using nurago's validator package and the
    tag rules declared on fields.
 2. The payload is JSON-encoded and sent as an authenticated HTTP POST
    (`Authorization: apikey <key>`).
 3. The request is executed through a write-oriented HTTP retrier.
 4. Non-200 responses are returned as errors with status details.

Health checks are implemented using Sleuth's documented behavior in absence of a
dedicated ping endpoint: the client performs a controlled registration call and
verifies expected 404 semantics and response content.

# Usage

	c, err := sleuth.New("https://app.sleuth.io/api/1", "my-org", apiKey)
	if err != nil {
	    return err
	}

	if err := c.HealthCheck(ctx); err != nil {
	    return err
	}

	err = c.SendDeployRegistration(ctx, &sleuth.DeployRegistrationRequest{
	    Deployment: "my-service",
	    Sha:        "abcdef1234567890abcdef1234567890abcdef12",
	})
	if err != nil {
	    return err
	}

# When To Use

  - A deployment pipeline reports to Sleuth.
  - Manual changes such as feature-flag flips should be tracked alongside
    deploys.
*/
package sleuth

// DeployRegistrationRequest defines the request for Deploy Registration.
type DeployRegistrationRequest struct {
	// Deployment is the Sleuth deploymnet ID as found in the Sleuth URL, following the prefix https://app.sleuth.io/org_slug/deployments/.
	Deployment string `json:"-" validate:"required,max=50"`

	// Sha is the Git SHA of the commit to be registered as a deploy.
	Sha string `json:"sha" validate:"required,max=40"`

	// Environment is the environment to register the deploy against.
	// If not provided Sleuth will use the default environment of the Project.
	Environment string `json:"environment,omitempty" validate:"omitempty,max=50"`

	// Date is the ISO 8601 deployment date and time string.
	Date string `json:"date,omitempty" validate:"omitempty,datetime=2006-01-02 15:04:05"`

	// Tags is a comma-delimited list of tags.
	// Default to tags calculated by matching paths defined in the .sleuth/TAGS file.
	Tags []string `json:"tags,omitempty" validate:"omitempty,max=50,dive,max=50,startswith=#"`

	// IgnoreIfDuplicate ignores duplicate SHA and do not return an error.
	IgnoreIfDuplicate bool `json:"ignore_if_duplicate,omitempty" validate:"omitempty"`

	// Email is the email address of the author.
	Email string `json:"email,omitempty" validate:"omitempty,email"`

	// Links contains key/value pair consisting of the link name and the link itself.
	Links map[string]string `json:"links,omitempty" validate:"omitempty,max=50,dive,url"`
}

// ManualChangeRequest defines the request for Manual Change.
type ManualChangeRequest struct {
	// Project is the Sleuth project ID as found in the Sleuth URL, following the prefix https://app.sleuth.io/org_slug/.
	Project string `json:"-" validate:"required,max=50"`

	// Name is the title for the manual change.
	Name string `json:"name" validate:"required,max=255"`

	// Description for manual changes. Omit if using SHA instead.
	Description string `json:"description,omitempty" validate:"omitempty,max=65535"`

	// Environment is the environment to register the deploy against.
	// If not provided Sleuth will use the default environment of the Project.
	Environment string `json:"environment,omitempty" validate:"omitempty,max=50"`

	// Tags is a comma-delimited list of tags.
	// Default to tags calculated by matching paths defined in the .sleuth/TAGS file.
	Tags []string `json:"tags,omitempty" validate:"omitempty,max=50,dive,max=50,startswith=#"`

	// Author is the email address of the change author.
	Author string `json:"author,omitempty" validate:"omitempty,email"`

	// Email is the email address of the user associated with the project receiving the manual change.
	Email string `json:"email,omitempty" validate:"omitempty,email"`
}

// IncidentType defines the valid values for an Incident Type.
type IncidentType string

const (
	// Triggered indicates a new incident.
	Triggered IncidentType = "triggered"

	// Resolved indicates the incident has been resolved.
	Resolved IncidentType = "resolved"

	// Reopened indicated the incident has been reopened.
	Reopened IncidentType = "reopened"
)

// CustomIncidentImpactRegistrationRequest defines the request for Custom Incident Impact Registration.
type CustomIncidentImpactRegistrationRequest struct {
	// Project is the Sleuth project ID as found in the Sleuth URL, following the prefix https://app.sleuth.io/org_slug/.
	Project string `json:"-" validate:"required,max=50"`

	// Environment is the environment to register the deploy against.
	// Found at the end of the URL of the Sleuth org when navigating to the target project
	// and selecting the target custom incident impact source: env_slug=ENVIRONMENT_SLUG.
	Environment string `json:"-" validate:"required,max=50"`

	// ImpactSource is found in the URL of the Sleuth org when navigating to the target project
	// and selecting the target custom incident impact source, just before the ?env_slug.
	ImpactSource string `json:"-" validate:"required,max=50"`

	// Type Valid types are triggered, resolved, and reopened.
	Type IncidentType `json:"type" validate:"required,oneof=triggered resolved reopened"`

	// ID is the unique (custom) incident identifier.
	ID string `json:"id" validate:"omitempty,max=50"`

	// Date is the ISO 8601 date and time string when the event occurred.
	// Defaults to the current time.
	Date string `json:"date,omitempty" validate:"omitempty,datetime=2006-01-02 15:04:05"`

	// EndedDate is the ISO 8601 date and time string when the event ended.
	// Use it with "type": "triggered" to register past incident event.
	EndedDate string `json:"ended_date,omitempty" validate:"omitempty,datetime=2006-01-02 15:04:05"`

	// Title is the human-readable title of the incident.
	Title string `json:"title,omitempty" validate:"omitempty,max=255"`

	// URL to the incident in the external system.
	URL string `json:"url,omitempty" validate:"omitempty,url"`
}

// CustomMetricImpactRegistrationRequest defines the request for Custom Metric Impact Registration.
type CustomMetricImpactRegistrationRequest struct {
	// ImpactID is the integer ID that can be found bny navigating in the Custom Metric Impact Source,
	// clicking the gearwheel icon in the top-right corner, and selecting "Show register details".
	ImpactID int `json:"-" validate:"required"`

	// Value is the metric value to be registered.
	// Zero is a legitimate metric value (e.g. a zero error rate), so no
	// "required" validation is applied (go-playground's required rejects
	// zero values); the field is always serialized.
	Value float64 `json:"value"`

	// Date is the ISO 8601 date and time string at which the metric value should be registered.
	// Defaults to the current time.
	Date string `json:"date,omitempty" validate:"omitempty,datetime=2006-01-02 15:04:05"`
}

// requestData aggregates the types for different API requests.
type requestData interface {
	DeployRegistrationRequest | ManualChangeRequest | CustomIncidentImpactRegistrationRequest | CustomMetricImpactRegistrationRequest
}
