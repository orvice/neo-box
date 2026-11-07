// Package connectx contains shared helpers for ConnectRPC handlers.
//
// Service methods in internal/application return *connect.Error values that
// the generated handler wrapper forwards directly. The helpers below cover the
// common shorthand cases (required argument, not found, internal-with-error, …)
// so callers don't need to spell out `connect.NewError(connect.CodeX, "...")`
// on every line.
package connectx

import (
	"context"

	"connectrpc.com/connect/v2"
)

// argumentHeader is the response header carrying the offending field name of
// an invalid argument, so the dashboard can highlight the right input.
const argumentHeader = "argument"

// setArgument records the offending field name on the RPC's response header.
// Connect v2 removed error metadata, so the value travels as a regular
// response header attached to the CallInfo; the transport still writes those
// headers when the handler returns an error.
func setArgument(ctx context.Context, name string) {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return
	}
	info.ResponseHeader().Set(argumentHeader, name)
}

// RequiredArgument returns `<name> is required` with CodeInvalidArgument and
// marks the argument in the response header.
func RequiredArgument(ctx context.Context, name string) *connect.Error {
	setArgument(ctx, name)
	return connect.Errorf(connect.CodeInvalidArgument, "%s is required", name)
}

// InvalidArgument returns `<name> <validation>` with CodeInvalidArgument and
// marks the argument in the response header.
func InvalidArgument(ctx context.Context, name, validation string) *connect.Error {
	setArgument(ctx, name)
	return connect.Errorf(connect.CodeInvalidArgument, "%s %s", name, validation)
}

// NotFound returns the given message with CodeNotFound.
func NotFound(msg string) *connect.Error {
	return connect.NewError(connect.CodeNotFound, msg)
}

// Internal returns the given message with CodeInternal.
func Internal(msg string) *connect.Error {
	return connect.NewError(connect.CodeInternal, msg)
}

// InternalWith wraps the underlying error with CodeInternal. The wire message
// is generic: v2 only serializes a locally authored *connect.Error, and
// internal text (SQL, connection strings, credentials) must not reach the
// client. The original error stays attached as a local cause for errors.Is /
// errors.As and for server-side logs.
func InternalWith(err error) *connect.Error {
	return connect.NewError(connect.CodeInternal, "internal error").WithCause(err)
}
