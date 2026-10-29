// Package defaults holds the workflow definitions the platform seeds,
// as documents: the review default and the release approval default.
//
// The default review workflow is data, not code (RFC 0006 §2.1 rule 2,
// §2.3): review.json is a glossa.workflow/v1 document, seeded per tenant
// on first use by the instance runner (wave 2) and from then on the
// tenant's own, editable and versioned like any other definition.
// Deleting it is allowed and means "no workflow", which is M4's
// behaviour.
//
// It reproduces the project setting `review_required` exactly by adding
// nothing to it: Localization already puts a new translation in
// needs_review when review is required, and only a reviewer's decision
// — through Localization's own ReviewTranslation port, with its own
// permission check — moves it on. The workflow waits for that decision
// and finishes. It sets no review state and creates no new way to
// approve text.
package defaults

import _ "embed"

//go:embed review.json
var review []byte

//go:embed release-approval.json
var releaseApproval []byte

// Review returns the default review definition's document.
func Review() []byte { return append([]byte(nil), review...) }

// ReleaseApprovalName is the release approval definition's name.
const ReleaseApprovalName = "release-approval"

// ReleaseApproval returns the release approval definition's document
// (RFC 0006 §5.1): a release request is pending while it asks the
// people its environment's `approval` names; when as many as it
// requires have granted — never the requester — it is approved and
// deployed as the last approver (pending → approved → deployed); one
// denial ends it denied, a withdrawal ends it withdrawn, and a deploy
// the publish gate refuses ends it refused.
//
// Unlike the review default it runs without being bound: every release
// request runs on the tenant's definition of this name unless a binding
// names another, and it is seeded on the tenant's first release request
// (again, if the tenant deleted it — a release request with no
// workflow would wait forever, and the requirement is Release's, not
// the chart's). A tenant may extend it like any definition: add stages,
// notify, set due dates. It cannot remove the requirement, because
// Release refuses to move a pointer until the environment's approval
// is met, whatever the chart says.
func ReleaseApproval() []byte { return append([]byte(nil), releaseApproval...) }
