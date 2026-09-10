package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type ValidationError struct {
	Code    string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func ValidateRequest(req Request) error {
	if req.V != Version1 {
		return &ValidationError{Code: "unsupported_version", Message: fmt.Sprintf("unsupported protocol version %d", req.V)}
	}
	if strings.TrimSpace(req.ID) == "" || len(req.ID) > 64 {
		return &ValidationError{Code: "invalid_request", Message: "request id is missing or too long"}
	}
	if strings.TrimSpace(req.Method) == "" || len(req.Method) > 128 {
		return &ValidationError{Code: "invalid_request", Message: "method is missing or too long"}
	}
	if len(req.Params) == 0 || !json.Valid(req.Params) || bytes.TrimSpace(req.Params)[0] != '{' {
		return &ValidationError{Code: "invalid_request", Message: "params must be a JSON object"}
	}
	return nil
}

func DecodeParams(raw json.RawMessage, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("params must contain exactly one object")
	}
	return nil
}
