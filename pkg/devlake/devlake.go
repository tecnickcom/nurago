/*
Package devlake provides a Go client for the DevLake Webhook API.

It sends deployment and incident lifecycle events from Go services to DevLake
with request validation and retries. The package includes typed request models
for deployments, deployment commits, incident creation, and incident close
operations, with validation tags aligned to expected webhook payload
constraints.

The client provides:

  - typed request payloads for deployment and incident webhook endpoints
  - helpers for SendDeployment, SendIncident, and SendIncidentClose
  - request validation before network calls using field/tag-based constraints
  - a health check for API reachability
  - configurable timeouts, retry attempts, and custom HTTP client injection
  - bearer-token authenticated JSON requests with contextual error wrapping

Based on the DevLake Webhook API documentation:
https://devlake.apache.org/docs/Plugins/webhook/

# When To Use

  - You feed DORA metrics into DevLake from a pipeline.
  - Payloads should be validated before they are sent.
*/
package devlake

import (
	"time"
)

// DeploymentCommitsRequest defines a deploymentCommits request item.
type DeploymentCommitsRequest struct {
	// DisplayTitle is a readable title for the deployment to this repo.
	DisplayTitle string `json:"displayTitle,omitempty" validate:"omitempty,max=255"`

	// RepoID is the repo ID.
	RepoID string `json:"repoID,omitempty" validate:"omitempty,max=255"`

	// RepoURL is the repo URL of the deployment commit.
	// If there is a row in the domain layer table repos where repos.url equals repo_url,
	// the repoId will be filled with repos.id.
	RepoURL string `json:"repoUrl" validate:"required,url"`

	// Name is the name of this commit.
	Name string `json:"name,omitempty" validate:"omitempty,max=255"`

	// RefName is the branch/tag to deploy.
	RefName string `json:"refName,omitempty" validate:"omitempty,max=255"`

	// CommitSha is the commit SHA that triggers the deploy in this repo.
	CommitSha string `json:"commitSha" validate:"required,min=40,max=255"`

	// CommitMsg is the commit SHA of the deployment commit message.
	CommitMsg string `json:"commitMsg,omitempty" validate:"omitempty,max=65536"`

	// Result is the result of the deploy to this repo. The default value is 'SUCCESS'.
	Result string `json:"result,omitempty" validate:"omitempty,max=50"`

	// Status is the commit status.
	Status string `json:"status,omitempty" validate:"omitempty,max=50"`

	// CreatedDate is the creation time of this commit.
	// E.g. 2020-01-01T12:00:00+00:00.
	CreatedDate *time.Time `json:"createdDate,omitempty" validate:"omitempty"`

	// StartedDate is the start time of the deploy to this repo.
	// E.g. 2020-01-01T12:00:00+00:00.
	StartedDate *time.Time `json:"startedDate" validate:"required"`

	// FinishedDate is the end time of the deploy to this repo.
	// E.g. 2020-01-01T12:00:00+00:00.
	FinishedDate *time.Time `json:"finishedDate" validate:"required"`
}

// DeploymentRequest defines the request to register a Deployment.
type DeploymentRequest struct {
	// ConnectionID is the ID of the DevLake connection where to send the deployment request.
	ConnectionID uint64 `json:"-" validate:"required"`

	// ID is the unique ID of table cicd_deployments.
	ID string `json:"id" validate:"required,max=255"`

	// DisplayTitle is a readable title for the deployment to this repo.
	DisplayTitle string `json:"displayTitle,omitempty" validate:"omitempty,max=255"`

	// Result is the deployment result, one of the values : SUCCESS, FAILURE, ABORT, MANUAL.
	// The default value is SUCCESS.
	Result string `json:"result,omitempty" validate:"omitempty,oneof=SUCCESS FAILURE ABORT MANUAL"`

	// Environment is the environment this deployment happens.
	// For example: PRODUCTION, STAGING, TESTING, DEVELOPMENT.
	// The default value is PRODUCTION.
	Environment string `json:"environment,omitempty" validate:"omitempty,oneof=PRODUCTION STAGING TESTING DEVELOPMENT"`

	// Name is the name of this deployment.
	Name string `json:"name,omitempty" validate:"omitempty,max=255"`

	// URL is the deployment URL.
	URL string `json:"url,omitempty" validate:"omitempty,url"`

	// CreatedDate is the time this deploy pipeline starts.
	// E.g. 2020-01-01T12:00:00+00:00.
	CreatedDate *time.Time `json:"createdDate,omitempty" validate:"omitempty"`

	// StartedDate is the time when the first deploy to a certain repo starts.
	// E.g. 2020-01-01T12:00:00+00:00.
	StartedDate *time.Time `json:"startedDate" validate:"required"`

	// FinishedDate is the time when the last deploy to a certain repo ends.
	// E.g. 2020-01-01T12:00:00+00:00.
	FinishedDate *time.Time `json:"finishedDate" validate:"required"`

	// DeploymentCommits is used for multiple commits in one deployment.
	DeploymentCommits []DeploymentCommitsRequest `json:"deploymentCommits,omitempty" validate:"omitempty,dive"`
}

// IncidentRequest defines the request to register an incident (issue).
type IncidentRequest struct {
	// ConnectionID is the ID of the DevLake connection where to send the incident request.
	ConnectionID uint64 `json:"-" validate:"required"`

	// URL is the Issue URL.
	URL string `json:"url,omitempty" validate:"omitempty,url"`

	// IssueKey is the Issue key. It needs to be unique in a connection.
	IssueKey string `json:"issueKey" validate:"required,max=255"`

	// Title is the issue title.
	Title string `json:"title" validate:"required,max=255"`

	// Description is the issue description.
	Description string `json:"description,omitempty" validate:"omitempty,max=65536"`

	// EpicKey is the issue epic key.
	EpicKey string `json:"epicKey,omitempty" validate:"omitempty,max=255"`

	// Type is the issue type, such as INCIDENT, BUG, REQUIREMENT.
	Type string `json:"type,omitempty" validate:"omitempty,max=50"`

	// Status is the issue status. Must be one of: TODO, DONE, IN_PROGRESS.
	Status string `json:"status" validate:"required,oneof=TODO DONE IN_PROGRESS"`

	// OriginalStatus is the status in your tool, such as: created, open, closed, ...
	OriginalStatus string `json:"originalStatus" validate:"required,max=255"`

	// StoryPoint
	StoryPoint float64 `json:"storyPoint,omitempty" validate:"omitempty"`

	// ResolutionDate is the date when the issue was resolved.
	// Format should be 2020-01-01T12:00:00+00:00.
	ResolutionDate *time.Time `json:"resolutionDate,omitempty" validate:"omitempty"`

	// CreatedDate is the date when the issue was created.
	// Format should be 2020-01-01T12:00:00+00:00.
	CreatedDate *time.Time `json:"createdDate" validate:"required"`

	// UpdatedDate is the date when the issue was last updated.
	// Format should be 2020-01-01T12:00:00+00:00.
	UpdatedDate *time.Time `json:"updatedDate,omitempty" validate:"omitempty"`

	// LeadTimeMinutes measures how long from this issue accepted to develop.
	LeadTimeMinutes uint `json:"leadTimeMinutes,omitempty" validate:"omitempty"`

	// ParentIssueKey is the key of the parent issue.
	ParentIssueKey string `json:"parentIssueKey,omitempty" validate:"omitempty,max=255"`

	// Priority is the issue priority.
	Priority string `json:"priority,omitempty" validate:"omitempty,max=255"`

	// OriginalEstimateMinutes is the original estimate in minutes.
	OriginalEstimateMinutes int64 `json:"originalEstimateMinutes,omitempty" validate:"omitempty"`

	// TimeSpentMinutes is the time spent on the issue in minutes.
	TimeSpentMinutes int64 `json:"timeSpentMinutes,omitempty" validate:"omitempty"`

	// TimeRemainingMinutes is the remaining time in minutes.
	TimeRemainingMinutes int64 `json:"timeRemainingMinutes,omitempty" validate:"omitempty"`

	// CreatorID is the user id of the issue creator.
	CreatorID string `json:"creatorId,omitempty" validate:"omitempty,max=255"`

	// CreatorName is the username of the creator.
	CreatorName string `json:"creatorName,omitempty" validate:"omitempty,max=255"`

	// AssigneeID is the ID of the assignee.
	AssigneeID string `json:"assigneeId,omitempty" validate:"omitempty,max=255"`

	// AssigneeName is the name of the assignee.
	AssigneeName string `json:"assigneeName,omitempty" validate:"omitempty,max=255"`

	// Severity is the severity of the issue.
	Severity string `json:"severity,omitempty" validate:"omitempty,max=255"`

	// Component is the affected component.
	Component string `json:"component,omitempty" validate:"omitempty,max=255"`
}

// IncidentRequestClose defines the request to close an incident (issue).
type IncidentRequestClose struct {
	// ConnectionID is the ID of the DevLake connection where to send the incident request.
	ConnectionID uint64 `json:"-" validate:"required"`

	// IssueKey is the Issue key. It needs to be unique in a connection.
	IssueKey string `json:"-" validate:"required,max=255"`
}

// requestData aggregates the types for different Webhook API requests.
type requestData interface {
	DeploymentRequest | IncidentRequest
}
