// SPDX-License-Identifier: MIT

// Package offline holds the commands that need no connection: the actions of a version,
// schema validation, the OCPP-J frame codec and certificate hashes.
package offline

import (
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/ocppcert"
	"github.com/otfabric/go-ocpp/ocppj"

	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/ocppx"
	"github.com/otfabric/ocppctl/internal/types"
)

func version(s string) (ocpp.Version, error) {
	v, err := ocppx.ParseVersion(s)
	if err != nil {
		return 0, errs.InvalidInput(errs.CodeUnsupportedVersion, err.Error(), err)
	}
	return v, nil
}

// ActionsConfig configures `actions`.
type ActionsConfig struct {
	Version      string `flag:"ocpp-version" env:"OCPPCTL_OCPP_VERSION" desc:"OCPP version: 1.6, 2.0.1 or 2.1"`
	From         string `flag:"from" desc:"Only the actions sent by: station or csms (DataTransfer, sent by both, is always listed)"`
	OutputFormat string `flag:"format" env:"OCPPCTL_FORMAT" desc:"Output format: text, json or table"`
}

// Actions lists the actions of a version.
func Actions(cfg *ActionsConfig) (*types.ActionsResult, error) {
	v, err := version(cfg.Version)
	if err != nil {
		return nil, err
	}
	switch cfg.From {
	case "", ocppx.FromStation, ocppx.FromCSMS:
	default:
		return nil, errs.InvalidInput(errs.CodeInvalidInput, fmt.Sprintf("invalid --from %q: want station or csms", cfg.From), nil)
	}
	res := &types.ActionsResult{Version: v.String(), Actions: []types.ActionInfo{}}
	for _, a := range ocppx.Actions(v) {
		if cfg.From != "" && a.From != cfg.From && a.From != ocppx.FromBoth {
			continue
		}
		res.Actions = append(res.Actions, types.ActionInfo{Name: a.Name, From: a.From, Kind: a.Kind})
	}
	return res, nil
}

// ValidateConfig configures `validate`.
type ValidateConfig struct {
	Version      string `flag:"ocpp-version" env:"OCPPCTL_OCPP_VERSION" desc:"OCPP version: 1.6, 2.0.1 or 2.1"`
	Response     bool   `flag:"response" desc:"Validate a response (CALLRESULT payload) instead of a request"`
	Payload      string `flag:"payload" desc:"Payload: JSON, @file, or - for stdin"`
	OutputFormat string `flag:"format" env:"OCPPCTL_FORMAT" desc:"Output format: text, json or table"`
	Action       string `flag:"-"`
}

// Validate validates a payload against the OCA schema of its action. An invalid payload
// is a result, not an error: the caller exits 3 when Valid is false.
func Validate(cfg *ValidateConfig, stdin io.Reader) (*types.ValidateResult, error) {
	v, err := version(cfg.Version)
	if err != nil {
		return nil, err
	}
	a, err := ocppx.Lookup(v, cfg.Action)
	if err != nil {
		return nil, errs.InvalidInput(errs.CodeUnknownAction, err.Error(), err)
	}
	if cfg.Response && a.Kind == "send" {
		return nil, errs.InvalidInput(errs.CodeInvalidFlagCombination, fmt.Sprintf("%s is a SEND: it has no response", a.Name), nil)
	}
	if cfg.Payload == "" {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, "--payload is required: JSON, @file, or - for stdin", nil)
	}
	p, err := conn.ReadPayload(cfg.Payload, stdin)
	if err != nil {
		return nil, err
	}
	res := &types.ValidateResult{Version: v.String(), Action: a.Name, Message: "request", Valid: true, Violations: []types.Violation{}}
	check := ocppx.ValidateRequest
	if cfg.Response {
		res.Message, check = "response", ocppx.ValidateResponse
	}
	if err := check(v, a.Name, p); err != nil {
		var ve *ocpp.ValidationError
		if !errors.As(err, &ve) {
			return nil, errs.Internal(err)
		}
		res.Valid = false
		res.Violations = append(res.Violations, types.Violation{Field: ve.Field, Reason: ve.Reason, ErrorCode: string(ve.Code)})
	}
	return res, nil
}

// FrameDecodeConfig configures `frame decode`.
type FrameDecodeConfig struct {
	Version      string `flag:"ocpp-version" env:"OCPPCTL_OCPP_VERSION" desc:"OCPP version whose framing applies: 1.6, 2.0.1 or 2.1"`
	NoValidate   bool   `flag:"no-validate" desc:"Do not validate the payload of a CALL or SEND against its schema"`
	OutputFormat string `flag:"format" env:"OCPPCTL_FORMAT" desc:"Output format: text, json or table"`
	Frame        string `flag:"-"`
}

// FrameDecode decodes an OCPP-J frame ("-" reads it from stdin).
func FrameDecode(cfg *FrameDecodeConfig, stdin io.Reader) (*types.FrameResult, error) {
	v, err := version(cfg.Version)
	if err != nil {
		return nil, err
	}
	data := []byte(cfg.Frame)
	if cfg.Frame == "-" {
		if data, err = io.ReadAll(stdin); err != nil {
			return nil, errs.InvalidInput(errs.CodeInputFileOpenFailed, err.Error(), err)
		}
	}
	data = []byte(strings.TrimSpace(string(data)))
	f, err := ocppj.Decode(data, v, ocppj.Limits{})
	if err != nil {
		return nil, errs.InvalidInput(errs.CodeProtocolViolation, err.Error(), err)
	}
	res := frameResult(v, f, data)
	if !cfg.NoValidate && (f.Type == ocppj.Call || f.Type == ocppj.Send) {
		ok := true
		if _, lerr := ocppx.Lookup(v, f.Action); lerr != nil {
			ok = false
			res.Violation = &types.Violation{Reason: lerr.Error(), ErrorCode: string(ocppj.RPCErrorCode(v))}
		} else if verr := ocppx.ValidateRequest(v, f.Action, f.Payload); verr != nil {
			ok = false
			var ve *ocpp.ValidationError
			if errors.As(verr, &ve) {
				res.Violation = &types.Violation{Field: ve.Field, Reason: ve.Reason, ErrorCode: string(ve.Code)}
			} else {
				res.Violation = &types.Violation{Reason: verr.Error()}
			}
		}
		res.PayloadValid = &ok
	}
	return res, nil
}

func frameResult(v ocpp.Version, f ocppj.Frame, data []byte) *types.FrameResult {
	return &types.FrameResult{
		Version: v.String(), Type: f.Type.String(), TypeNumber: int(f.Type), MessageID: f.ID, Action: f.Action,
		Payload: f.Payload, ErrorCode: string(f.ErrorCode), Description: f.Description, Details: f.Details, Frame: string(data),
	}
}

// FrameEncodeConfig configures `frame encode`.
type FrameEncodeConfig struct {
	Version      string `flag:"ocpp-version" env:"OCPPCTL_OCPP_VERSION" desc:"OCPP version whose framing applies: 1.6, 2.0.1 or 2.1"`
	Type         string `flag:"type" desc:"Message type: call, callresult, callerror, callresulterror or send"`
	ID           string `flag:"id" desc:"Message id (at most 36 characters)"`
	Action       string `flag:"action" desc:"Action of a call or send"`
	Payload      string `flag:"payload" desc:"Payload of a call, callresult or send: JSON, @file, or - for stdin (default {})"`
	ErrorCode    string `flag:"error-code" desc:"Error code of a callerror or callresulterror"`
	Description  string `flag:"description" desc:"Error description of a callerror or callresulterror"`
	Details      string `flag:"details" desc:"Error details of a callerror or callresulterror: a JSON object (default {})"`
	OutputFormat string `flag:"format" env:"OCPPCTL_FORMAT" desc:"Output format: text, json or table"`
}

// FrameTypes are the --type values of `frame encode`.
var FrameTypes = map[string]ocppj.MessageType{
	"call": ocppj.Call, "callresult": ocppj.CallResult, "callerror": ocppj.CallError,
	"callresulterror": ocppj.CallResultError, "send": ocppj.Send,
}

// FrameEncode builds an OCPP-J frame and checks it by decoding it again.
func FrameEncode(cfg *FrameEncodeConfig, stdin io.Reader) (*types.FrameResult, error) {
	v, err := version(cfg.Version)
	if err != nil {
		return nil, err
	}
	t, ok := FrameTypes[strings.ToLower(cfg.Type)]
	if !ok {
		return nil, errs.InvalidInput(errs.CodeInvalidInput,
			fmt.Sprintf("invalid --type %q: want call, callresult, callerror, callresulterror or send", cfg.Type), nil)
	}
	if !t.Allowed(v) {
		return nil, errs.InvalidInput(errs.CodeUnsupportedVersion, fmt.Sprintf("%s frames do not exist in OCPP %s", cfg.Type, v), nil)
	}
	if cfg.ID == "" {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, "--id is required", nil)
	}
	f := ocppj.Frame{Type: t, ID: cfg.ID}
	switch t {
	case ocppj.Call, ocppj.Send:
		if cfg.Action == "" {
			return nil, errs.InvalidInput(errs.CodeInvalidInput, "--action is required for a "+cfg.Type, nil)
		}
		f.Action = cfg.Action
		fallthrough
	case ocppj.CallResult:
		if f.Payload, err = conn.ReadPayload(cfg.Payload, stdin); err != nil {
			return nil, err
		}
	default:
		if cfg.ErrorCode == "" {
			return nil, errs.InvalidInput(errs.CodeInvalidInput, "--error-code is required for a "+cfg.Type, nil)
		}
		if !ocppj.ValidErrorCode(v, ocpp.ErrorCode(cfg.ErrorCode)) {
			return nil, errs.InvalidInput(errs.CodeInvalidInput, fmt.Sprintf("%q is not an error code of OCPP %s", cfg.ErrorCode, v), nil)
		}
		f.ErrorCode, f.Description = ocpp.ErrorCode(cfg.ErrorCode), cfg.Description
		f.Details = json.RawMessage(`{}`)
		if cfg.Details != "" {
			if f.Details, err = conn.ReadPayload(cfg.Details, stdin); err != nil {
				return nil, err
			}
		}
	}
	data, err := ocppj.Encode(f)
	if err != nil {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, err.Error(), err)
	}
	if _, err := ocppj.Decode(data, v, ocppj.Limits{}); err != nil {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, err.Error(), err)
	}
	return frameResult(v, f, data), nil
}

// CertHashConfig configures `cert hash`.
type CertHashConfig struct {
	Cert         string `flag:"cert" desc:"PEM file of the certificate"`
	Issuer       string `flag:"issuer" desc:"PEM file of the issuer's certificate (default: the certificate itself, for a self-signed root)"`
	Hash         string `flag:"hash" desc:"Hash algorithm: sha256, sha384 or sha512"`
	OutputFormat string `flag:"format" env:"OCPPCTL_FORMAT" desc:"Output format: text, json or table"`
}

// HashAlgorithms are the --hash values of `cert hash`.
var HashAlgorithms = map[string]crypto.Hash{"sha256": crypto.SHA256, "sha384": crypto.SHA384, "sha512": crypto.SHA512}

// CertHash computes the CertificateHashData of a certificate.
func CertHash(cfg *CertHashConfig) (*types.CertHashResult, error) {
	alg, ok := HashAlgorithms[strings.ToLower(cfg.Hash)]
	if !ok {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, fmt.Sprintf("invalid --hash %q: want sha256, sha384 or sha512", cfg.Hash), nil)
	}
	cert, err := readCert("--cert", cfg.Cert)
	if err != nil {
		return nil, err
	}
	issuer := cert
	if cfg.Issuer != "" {
		if issuer, err = readCert("--issuer", cfg.Issuer); err != nil {
			return nil, err
		}
	}
	if err := cert.CheckSignatureFrom(issuer); err != nil {
		return nil, errs.InvalidInput(errs.CodeInvalidInput,
			fmt.Sprintf("the certificate is not signed by --issuer (%s): the hash would name the wrong certificate", issuer.Subject), err)
	}
	h, err := ocppcert.Hash(cert, issuer, alg)
	if err != nil {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, err.Error(), err)
	}
	return &types.CertHashResult{
		Certificate: cert.Subject.String(), HashAlgorithm: h.HashAlgorithm, IssuerNameHash: h.IssuerNameHash,
		IssuerKeyHash: h.IssuerKeyHash, SerialNumber: h.SerialNumber,
		OCPP: map[string]string{"hashAlgorithm": h.HashAlgorithm, "issuerNameHash": h.IssuerNameHash,
			"issuerKeyHash": h.IssuerKeyHash, "serialNumber": h.SerialNumber},
	}, nil
}

func readCert(flag, file string) (*x509.Certificate, error) {
	if file == "" {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, flag+" is required", nil)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, errs.InvalidInput(errs.CodeInputFileOpenFailed, fmt.Sprintf("%s: %v", flag, err), err)
	}
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			return nil, errs.InvalidInput(errs.CodeInputDecodeFailed, fmt.Sprintf("%s: no PEM CERTIFICATE block in %s", flag, file), nil)
		}
		if blk.Type == "CERTIFICATE" {
			c, err := x509.ParseCertificate(blk.Bytes)
			if err != nil {
				return nil, errs.InvalidInput(errs.CodeInputDecodeFailed, fmt.Sprintf("%s: %v", flag, err), err)
			}
			return c, nil
		}
	}
}
