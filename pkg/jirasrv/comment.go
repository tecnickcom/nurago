package jirasrv

import "github.com/tecnickcom/nurago/pkg/timeutil"

// Comment models a Jira issue comment payload.
// Ref.: https://docs.atlassian.com/software/jira/docs/api/REST/9.17.0
type Comment struct {
	Self         string                             `json:"self"`
	ID           string                             `json:"id"`
	Author       *User                              `json:"author,omitempty"`
	Body         string                             `json:"body"`
	RenderedBody string                             `json:"renderedBody,omitempty"`
	UpdateAuthor *User                              `json:"updateAuthor,omitempty"`
	Created      *timeutil.DateTime[timeutil.TJira] `json:"created"`
	Updated      *timeutil.DateTime[timeutil.TJira] `json:"updated"`
	Visibility   *Visibility                        `json:"visibility,omitempty"`
	Properties   []EntityProperty                   `json:"properties,omitempty"`
}

// User describes a Jira account reference returned by comment endpoints.
type User struct {
	Self        string            `json:"self"`
	Name        string            `json:"name"`
	Key         string            `json:"key"`
	Email       string            `json:"emailAddress"`
	AvatarURLs  map[string]string `json:"avatarUrls"`
	DisplayName string            `json:"displayName"`
	Active      bool              `json:"active"`
	TimeZone    string            `json:"timeZone"`
}

// Visibility defines comment audience restrictions (for example group or role).
type Visibility struct {
	Type  string `json:"type"` // "group" or "role"
	Value string `json:"value"`
}
