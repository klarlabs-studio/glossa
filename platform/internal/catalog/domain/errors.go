package domain

import "errors"

// Domain errors. The HTTP adapter maps each to a problem code.
var (
	ErrInvalidID          = errors.New("catalog: invalid id")
	ErrInvalidSlug        = errors.New("catalog: slug must be lowercase letters, digits and inner hyphens, at most 63")
	ErrInvalidName        = errors.New("catalog: name must be 1-200 characters")
	ErrInvalidPlatform    = errors.New("catalog: platform must be web, api, ios, android or other")
	ErrInvalidKey         = errors.New("catalog: message key must be dotted segments of [a-z0-9_-], at most 200 characters")
	ErrInvalidNamespace   = errors.New("catalog: namespace must be 1-64 of [a-z0-9_-], starting with a letter or digit")
	ErrInvalidDescription = errors.New("catalog: description must be at most 2000 characters")
	ErrInvalidMaxLength   = errors.New("catalog: max_length must be between 1 and 100000")
	ErrMessageObsolete    = errors.New("catalog: message is obsolete")
	ErrInvalidBranchName  = errors.New("catalog: branch name must be a valid Git branch name of at most 255 bytes")
	ErrInvalidPush        = errors.New("catalog: invalid branch push")
	ErrInvalidPreviewURL  = errors.New("catalog: preview URL must be an absolute http(s) URL of at most 2000 bytes")
	ErrBranchMerged       = errors.New("catalog: branch is merged")
	ErrBranchTransition   = errors.New("catalog: branch can't make that change in its state")
)
