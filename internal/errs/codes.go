// SPDX-License-Identifier: MIT

package errs

// Stable machine-oriented codes for JSON error.code and scripts (docs/exitcodes.md).
const (
	CodeInvalidFormat          = "invalid_format"
	CodeInvalidInput           = "invalid_input"
	CodeInvalidURL             = "invalid_url"
	CodeInvalidFlagCombination = "invalid_flag_combination"
	CodeInvalidConfig          = "invalid_config"
	CodeInvalidPayload         = "invalid_payload"
	CodeUnknownAction          = "unknown_action"
	CodeUnsupportedVersion     = "unsupported_version"

	CodeTransportConnectFailed = "transport_connect_failed"
	CodeTransportTLSFailed     = "transport_tls_failed"
	CodeHandshakeRefused       = "handshake_refused"
	CodeNoCommonVersion        = "no_common_version"
	CodeConnectionLost         = "connection_lost"
	CodeListenFailed           = "listen_failed"
	CodeControlSocketFailed    = "control_socket_failed"
	CodeStationNotConnected    = "station_not_connected"
	// A command that may change a real station was not confirmed with --yes.
	CodeConfirmationRequired = "confirmation_required"

	// No response arrived in time to a request that does not change state.
	CodeRequestTimeout = "request_timeout"
	// No response arrived to a request that may have changed state: it is not resent.
	CodeOutcomeUnknown = "outcome_unknown"

	// The peer answered with a CALLERROR.
	CodeCallError = "call_error"
	// The peer's response does not conform to the OCA schema.
	CodeInvalidResponse = "invalid_response"
	// The peer violated OCPP-J framing.
	CodeProtocolViolation = "protocol_violation"
	// A boot that had to be accepted was not.
	CodeBootNotAccepted = "boot_not_accepted"

	CodeContextCanceled = "context_canceled"

	CodeJSONEncodeFailed       = "json_encode_failed"
	CodeTableRenderFailed      = "table_render_failed"
	CodeOutputWriteFailed      = "output_write_failed"
	CodeOutputFileWriteFailed  = "output_file_write_failed"
	CodeOutputFileCreateFailed = "output_file_create_failed"
	CodeInputFileOpenFailed    = "input_file_open_failed"
	CodeInputDecodeFailed      = "input_decode_failed"

	CodeInternalUnexpected = "internal_unexpected"
)
