package domain

// Context's domain events (platform/README.md, "Domain events").
const (
	AggregateBuild   = "build"
	AggregateCapture = "capture"

	// EventBuildIngested: a build's usages were stored. Intelligence
	// refreshes message context and Integration updates the PR check
	// (RFC 0004 §2.2, §6.4).
	EventBuildIngested = "context.build.ingested"
	// EventCaptureIngested: a capture and its regions were stored.
	EventCaptureIngested = "context.capture.ingested"
)

// BuildIngested is the payload of context.build.ingested.
type BuildIngested struct {
	BuildID         string `json:"build_id"`
	ProjectID       string `json:"project_id"`
	ApplicationID   string `json:"application_id"`
	Commit          string `json:"commit"`
	Branch          string `json:"branch"`
	OnDefaultBranch bool   `json:"on_default_branch"`
	Source          string `json:"source"`
	Usages          int    `json:"usages"`
	// UnknownKeys counts usages whose key the catalog doesn't know.
	UnknownKeys int    `json:"unknown_keys"`
	By          string `json:"by"`
}

// BuildIngestedOf builds the payload for b.
func BuildIngestedOf(b Build, unknownKeys int) BuildIngested {
	return BuildIngested{
		BuildID: b.ID.String(), ProjectID: b.ProjectID.String(), ApplicationID: b.ApplicationID.String(),
		Commit: b.Commit.String(), Branch: b.Branch.String(), OnDefaultBranch: b.OnDefaultBranch,
		Source: string(b.Source), Usages: b.UsageCount, UnknownKeys: unknownKeys, By: b.CreatedBy,
	}
}

// CaptureIngested is the payload of context.capture.ingested.
type CaptureIngested struct {
	CaptureID     string `json:"capture_id"`
	BuildID       string `json:"build_id"`
	ProjectID     string `json:"project_id"`
	ApplicationID string `json:"application_id"`
	Route         string `json:"route"`
	Locale        string `json:"locale"`
	ImageDigest   string `json:"image_digest"`
	Regions       int    `json:"regions"`
	By            string `json:"by"`
}

// CaptureIngestedOf builds the payload for c, a capture of b.
func CaptureIngestedOf(c Capture, b Build) CaptureIngested {
	return CaptureIngested{
		CaptureID: c.ID.String(), BuildID: b.ID.String(), ProjectID: c.ProjectID.String(),
		ApplicationID: b.ApplicationID.String(), Route: c.Route, Locale: c.Locale.String(),
		ImageDigest: c.Image.Digest.String(), Regions: len(c.Regions), By: c.CreatedBy,
	}
}
