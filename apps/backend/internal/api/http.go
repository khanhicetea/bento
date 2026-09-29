// Package api serves the versioned REST control plane (/api/v1), the
// authenticated scheduler gateway, terminal and log streams, and the
// embedded web UI. Handlers delegate every mutation to the operations
// controller; none owns a lifecycle implementation.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/store"
)

// MaxBodyBytes bounds JSON request bodies.
const MaxBodyBytes = 1 << 20

const statusClientClosedRequest = 499

var errUnsupportedMedia = errors.New("content type must be application/json")

// decode strictly parses one JSON object: bounded size, no unknown fields,
// no trailing data.
func decode(w http.ResponseWriter, r *http.Request, out any) error {
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		return errUnsupportedMedia
	}
	body := http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	raw, err := io.ReadAll(body)
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return &apiError{status: http.StatusRequestEntityTooLarge, code: dto.ErrorCodeTooLarge, msg: "request body too large"}
		}
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return &apiError{status: http.StatusBadRequest, code: dto.ErrorCodeValidation, msg: "invalid JSON: " + jsonErr(err)}
	}
	if dec.More() {
		return &apiError{status: http.StatusBadRequest, code: dto.ErrorCodeValidation, msg: "invalid JSON: trailing data after object"}
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return &apiError{status: http.StatusBadRequest, code: dto.ErrorCodeValidation, msg: "invalid JSON: trailing data after object"}
	}
	return nil
}

func jsonErr(err error) string {
	if ute, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return fmt.Sprintf("field %q has the wrong type or is out of range", ute.Field)
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "json: unknown field") {
		return strings.TrimPrefix(msg, "json: ")
	}
	if _, ok := errors.AsType[*json.SyntaxError](err); ok {
		return "malformed JSON"
	}
	return "malformed request"
}

type apiError struct {
	status int
	code   dto.ErrorCode
	msg    string
	fields []dto.FieldError
}

func (e *apiError) Error() string { return e.msg }

func badRequest(msg string) *apiError {
	return &apiError{status: http.StatusBadRequest, code: dto.ErrorCodeValidation, msg: msg}
}

func notFound(msg string) *apiError {
	return &apiError{status: http.StatusNotFound, code: dto.ErrorCodeNotFound, msg: msg}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

// writeError maps domain/store/operation errors to stable codes. Unexpected
// errors return a generic message; details go to the server log only.
func writeError(w http.ResponseWriter, log *slog.Logger, err error) {
	var ae *apiError
	var ve domain.ValidationErrors
	var oe *operations.OpError
	switch {
	case errors.As(err, &ae):
	case errors.As(err, &ve):
		ae = &apiError{status: http.StatusUnprocessableEntity, code: dto.ErrorCodeValidation, msg: "validation failed"}
		for _, f := range ve {
			ae.fields = append(ae.fields, dto.FieldError{Field: f.Field, Message: f.Message})
		}
	case errors.Is(err, errUnsupportedMedia):
		ae = &apiError{status: http.StatusUnsupportedMediaType, code: dto.ErrorCodeValidation, msg: err.Error()}
	case errors.Is(err, store.ErrNotFound):
		ae = &apiError{status: http.StatusNotFound, code: dto.ErrorCodeNotFound, msg: cleanMsg(err)}
	case errors.Is(err, store.ErrConflict):
		ae = &apiError{status: http.StatusConflict, code: dto.ErrorCodeConflict, msg: cleanMsg(err)}
	case errors.Is(err, operations.ErrPrecondition):
		ae = &apiError{status: http.StatusConflict, code: dto.ErrorCodePrecondition, msg: cleanMsg(err)}
	case errors.Is(err, operations.ErrConfirmation):
		ae = &apiError{status: http.StatusBadRequest, code: dto.ErrorCodeConfirmation, msg: cleanMsg(err)}
	case errors.Is(err, store.ErrUIDExhausted):
		ae = &apiError{status: http.StatusConflict, code: dto.ErrorCodeConflict, msg: err.Error()}
	case errors.As(err, &oe):
		ae = &apiError{status: http.StatusServiceUnavailable, code: dto.ErrorCodeUnavailable, msg: oe.Message}
	case errors.Is(err, context.Canceled):
		log.Debug("request canceled by client", "err", err)
		ae = &apiError{status: statusClientClosedRequest, code: dto.ErrorCodeUnavailable, msg: "request canceled"}
	default:
		log.Error("request failed", "err", err)
		ae = &apiError{status: http.StatusInternalServerError, code: dto.ErrorCodeInternal, msg: "internal error; see backend log"}
	}
	writeJSON(w, ae.status, dto.ErrorResponse{Error: dto.ErrorBody{Code: ae.code, Message: ae.msg, Fields: ae.fields}})
}

// cleanMsg strips sentinel prefixes such as "not found: ".
func cleanMsg(err error) string {
	msg := err.Error()
	for _, p := range []string{"not found: ", "conflict: ", "precondition failed: ", "confirmation required: "} {
		msg = strings.TrimPrefix(msg, p)
	}
	return msg
}

var idemPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

// idempotencyKey reads the optional Idempotency-Key header.
func idempotencyKey(r *http.Request) (string, error) {
	k := r.Header.Get("Idempotency-Key")
	if k == "" {
		return "", nil
	}
	if !idemPattern.MatchString(k) {
		return "", badRequest("Idempotency-Key must be 8-128 characters of [A-Za-z0-9._:-]")
	}
	return k, nil
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)

func pathID(r *http.Request, name string) (string, error) {
	v := r.PathValue(name)
	if !idPattern.MatchString(v) {
		return "", badRequest("invalid identifier in path")
	}
	return v, nil
}
