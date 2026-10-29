package glossa

import "errors"

// ErrorType classifies runtime errors (runtimes/SPEC.md §6).
type ErrorType string

// Error types.
const (
	ErrorNetwork        ErrorType = "network"
	ErrorIntegrity      ErrorType = "integrity"
	ErrorSignature      ErrorType = "signature"
	ErrorSchema         ErrorType = "schema"
	ErrorFormat         ErrorType = "format"
	ErrorMissingMessage ErrorType = "missing-message"
)

// Sentinels for load failures; errorType maps them to an ErrorType.
var (
	errNetwork   = errors.New("glossa: network")
	errIntegrity = errors.New("glossa: integrity")
	errSignature = errors.New("glossa: signature")
	errSchema    = errors.New("glossa: schema")
)

// errorType classifies a load error. Anything unclassified is a network
// error: the only other thing a load does is talk to the edge.
func errorType(err error) ErrorType {
	switch {
	case errors.Is(err, errIntegrity):
		return ErrorIntegrity
	case errors.Is(err, errSignature):
		return ErrorSignature
	case errors.Is(err, errSchema):
		return ErrorSchema
	default:
		return ErrorNetwork
	}
}
