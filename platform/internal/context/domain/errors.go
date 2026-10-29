package domain

import "errors"

// Domain errors. The HTTP adapter maps each to a problem code.
var (
	ErrInvalidUpload  = errors.New("context: invalid glossa.usages/v1 document")
	ErrUploadTooLarge = errors.New("context: the usages document is larger than 20 MB")
	ErrTooManyUsages  = errors.New("context: a build holds at most 100000 usages")
	ErrInvalidSource  = errors.New("context: source must be plugin, extract, runtime or capture")
	ErrInvalidBranch  = errors.New("context: invalid branch name")
	ErrInvalidCommit  = errors.New("context: commit must be 7 to 64 hexadecimal digits")
	ErrInvalidDigest  = errors.New("context: digest must be 64 lowercase hexadecimal digits (SHA-256)")

	ErrInvalidCapture  = errors.New("context: invalid capture")
	ErrInvalidRegion   = errors.New("context: invalid region")
	ErrTooManyRegions  = errors.New("context: a capture holds at most 10000 regions")
	ErrTooManyCaptures = errors.New("context: a build holds at most 500 captures")

	ErrInvalidCaptures  = errors.New("context: invalid glossa.captures/v1 upload")
	ErrManifestTooLarge = errors.New("context: the captures manifest is larger than 20 MB")
	// ErrInvalidImage means an uploaded image isn't the PNG its
	// manifest entry describes.
	ErrInvalidImage = errors.New("context: invalid capture image")
	// ErrImageTooLarge means an image exceeds 10 MB or 40 megapixels.
	ErrImageTooLarge = errors.New("context: a capture image is at most 10 MB and 40 megapixels")

	// ErrTooManyFindings means a capture, or the whole upload, carries
	// more visual findings than may be stored (RFC 0005 §10).
	ErrTooManyFindings = errors.New("context: a capture holds at most 500 visual findings and an upload at most 10000")
)
