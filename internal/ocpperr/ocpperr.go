// SPDX-License-Identifier: MIT

// Package ocpperr classifies the errors of go-ocpp as typed CLI errors with a stable kind,
// code and exit code (docs/exitcodes.md), with protocol detail for the JSON envelope.
package ocpperr

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/client"
	"github.com/otfabric/go-ocpp/ocppj"

	"github.com/otfabric/ocppctl/internal/errs"
)

// CallErrorDetail is the "ocpp" object of a CALLERROR.
type CallErrorDetail struct {
	ErrorCode   string `json:"error_code"`
	Description string `json:"description"`
	Details     any    `json:"details,omitempty"`
}

// ValidationDetail is the "ocpp" object of a schema violation.
type ValidationDetail struct {
	Field     string `json:"field,omitempty"`
	Reason    string `json:"reason"`
	ErrorCode string `json:"error_code"`
}

// HandshakeDetail is the "ocpp" object of a refused connection.
type HandshakeDetail struct {
	HTTPStatus int    `json:"http_status"`
	Meaning    string `json:"meaning"`
}

// Meaning explains an HTTP status a CSMS refuses a station's handshake with.
func Meaning(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "authentication failed: wrong password, or no credentials (security profiles 1 and 2)"
	case http.StatusForbidden:
		return "forbidden: the CSMS refuses this station or security profile"
	case http.StatusNotFound:
		return "unknown station identity, or wrong endpoint path"
	case http.StatusConflict:
		return "the station is already connected"
	case http.StatusBadRequest:
		return "the request was refused (for OCPP 2.x: an identity that is not a valid identifierString)"
	case http.StatusServiceUnavailable:
		return "the CSMS is full or shutting down: try later"
	}
	return http.StatusText(status)
}

// Wrap classifies err. action is the action of a CALL in flight ("" for a connection), and
// stateChanging says whether its request may have changed state at the receiver: a timeout
// of such a request is "outcome_unknown", never retried. A typed error is returned as it
// is.
func Wrap(err error, action string, stateChanging bool) error {
	if err == nil {
		return nil
	}
	if _, ok := errs.As(err); ok {
		return err
	}
	var (
		ce        *ocpp.CallError
		ve        *ocpp.ValidationError
		he        *client.HandshakeError
		certErr   *tls.CertificateVerificationError
		unknownCA x509.UnknownAuthorityError
		hostname  x509.HostnameError
		record    tls.RecordHeaderError
		de        *ocppj.DecodeError
	)
	what := action
	if what == "" {
		what = "the connection"
	}
	switch {
	case errors.Is(err, context.Canceled):
		return errs.New(errs.KindInternal, errs.CodeContextCanceled, "interrupted", err)
	case errors.As(err, &ce):
		return errs.New(errs.KindNegative, errs.CodeCallError,
			fmt.Sprintf("%s: the peer answered with CALLERROR %s%s", action, ce.Code, suffix(ce.Description)), err).
			WithOCPP(CallErrorDetail{ErrorCode: string(ce.Code), Description: ce.Description, Details: rawOrNil(ce.Details)})
	case errors.As(err, &ve) && errors.Is(err, ocpp.ErrProtocol):
		return errs.New(errs.KindProtocol, errs.CodeInvalidResponse,
			fmt.Sprintf("%s: the response does not conform to the OCA schema: %v", action, ve), err).
			WithOCPP(ValidationDetail{Field: ve.Field, Reason: ve.Reason, ErrorCode: string(ve.Code)})
	case errors.As(err, &ve):
		return errs.New(errs.KindInvalidInput, errs.CodeInvalidPayload,
			fmt.Sprintf("%s: the request does not conform to the OCA schema: %v (nothing was sent)", action, ve), err).
			WithOCPP(ValidationDetail{Field: ve.Field, Reason: ve.Reason, ErrorCode: string(ve.Code)})
	case errors.As(err, &he):
		return errs.New(errs.KindTransport, errs.CodeHandshakeRefused,
			fmt.Sprintf("connection refused by the CSMS: HTTP %d: %s", he.StatusCode, Meaning(he.StatusCode)), err).
			WithOCPP(HandshakeDetail{HTTPStatus: he.StatusCode, Meaning: Meaning(he.StatusCode)})
	case errors.Is(err, ocpp.ErrConnectFailed) && errors.Is(err, ocpp.ErrUnsupportedVersion):
		return errs.New(errs.KindTransport, errs.CodeNoCommonVersion,
			"connection refused: the CSMS supports none of the OCPP versions offered (--ocpp-version)", err)
	case errors.As(err, &certErr), errors.As(err, &unknownCA), errors.As(err, &hostname), errors.As(err, &record):
		return errs.New(errs.KindTransport, errs.CodeTransportTLSFailed, fmt.Sprintf("TLS error: %v", err), err)
	case errors.Is(err, ocpp.ErrConnectFailed):
		return errs.New(errs.KindTransport, errs.CodeTransportConnectFailed, fmt.Sprintf("connection error: %v", err), err)
	case errors.Is(err, ocpp.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		if stateChanging && action != "" {
			return errs.New(errs.KindTimeout, errs.CodeOutcomeUnknown,
				fmt.Sprintf("%s: no response in time; the request may have been carried out, and is not sent again", action), err)
		}
		return errs.New(errs.KindTimeout, errs.CodeRequestTimeout, fmt.Sprintf("%s: no response in time", what), err)
	case errors.Is(err, ocpp.ErrConnectionLost), errors.Is(err, ocpp.ErrNotConnected), errors.Is(err, ocpp.ErrClosed):
		return errs.New(errs.KindTransport, errs.CodeConnectionLost, fmt.Sprintf("connection error: %v", err), err)
	case errors.Is(err, ocpp.ErrUnsupportedVersion):
		return errs.New(errs.KindInvalidInput, errs.CodeUnsupportedVersion, err.Error(), err)
	case errors.Is(err, ocpp.ErrWrongDirection):
		return errs.New(errs.KindInvalidInput, errs.CodeUnknownAction, err.Error(), err)
	case errors.Is(err, ocpp.ErrInvalidOption):
		return errs.New(errs.KindInvalidInput, errs.CodeInvalidConfig, err.Error(), err)
	case errors.Is(err, ocpp.ErrProtocol), errors.As(err, &de):
		return errs.New(errs.KindProtocol, errs.CodeProtocolViolation, fmt.Sprintf("protocol error: %v", err), err)
	}
	return errs.Internal(err)
}

func suffix(desc string) string {
	if desc == "" {
		return ""
	}
	return ": " + desc
}

func rawOrNil(b []byte) any {
	if len(b) == 0 || string(b) == "{}" {
		return nil
	}
	return jsonRaw(b)
}
