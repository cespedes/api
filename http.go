package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Exported functions:
//   - func HTTPError(code int, f any, a ...any) error

// Exported types:
//   - type HTTPStatus interface { ... }

// These functions are used by other files in this package:
//   - httpError()
//   - httpCodeError()

// Dependencies:
//   - HTTPError     -> errHTTPStatus
//   - HTTPStatus    -> (none)
//   - httpError     -> httpMessage
//   - httpCodeError -> HTTPError, httpError
//   - httpMessage   -> (none)

// Errors...:
type errHTTPStatus struct {
	Status int
	Err    error
}

func (e errHTTPStatus) Error() string {
	return e.Err.Error()
}

func (e errHTTPStatus) Unwrap() error {
	return e.Err
}

func (e errHTTPStatus) HTTPStatus() int {
	return e.Status
}

// HTTPError returns an error with an embedded HTTP status code
func HTTPError(code int, f any, a ...any) error {
	var err error
	if e, ok := f.(error); ok {
		err = e
	} else if s, ok := f.(string); ok && len(a) > 0 {
		err = fmt.Errorf(s, a...)
	} else {
		err = errors.New(fmt.Sprint(f))
	}

	return errHTTPStatus{
		Status: code,
		Err:    err,
	}
}

// If HTTPStatus is implemented by the output or the error returned
// by a handler used in [Handler], it is user as the HTTP Status code
// to be returned.
type HTTPStatus interface {
	HTTPStatus() int
}

// httpError sends a HTTP error as a response.
//
// If the error returned by the function implements HTTPStatus,
// it is used as the HTTP Status code to be returned.
func httpError(w http.ResponseWriter, f any, a ...any) {
	var err error
	if e, ok := f.(error); ok {
		err = e
	} else if s, ok := f.(string); ok {
		err = fmt.Errorf(s, a...)
	} else {
		err = errors.New(fmt.Sprint(f))
	}

	var es interface{ SQLState() string }
	if errors.As(err, &es) {
		w.Header().Set("X-SQL-Error", fmt.Sprintf("%s %s", es.SQLState(), err.Error()))
	}

	var eh HTTPStatus

	code := http.StatusBadRequest
	switch {
	case errors.As(err, &eh):
		code = eh.HTTPStatus()
	case errors.Is(err, sql.ErrNoRows):
		code = http.StatusNotFound
		err = errors.New("not found")
	}

	httpMessage(w, code, "error", err.Error())
}

// httpCodeError sends a HTTP error as a response.
func httpCodeError(w http.ResponseWriter, code int, f any, a ...any) {
	err := HTTPError(code, f, a...).(errHTTPStatus)
	httpError(w, err)
}

func httpMessage(w http.ResponseWriter, code int, label string, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, "{%q: %q}\n", label, msg)
}

// output sends a JSON-encoded output.
func output(w http.ResponseWriter, out any) {
	if err, ok := out.(error); ok {
		httpError(w, err)
		return
	}

	// if the returned type is a string, output it as a "info" message:
	if s, ok := out.(string); ok {
		httpMessage(w, http.StatusOK, "info", s)
		return
	}

	// if the returned type is a []byte, output it directly:
	if b, ok := out.([]byte); ok {
		w.Write(b)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	code := http.StatusOK
	if hs, ok := out.(HTTPStatus); ok {
		code = hs.HTTPStatus()
	}
	w.WriteHeader(code)

	e := json.NewEncoder(w)
	err := e.Encode(out)
	if err != nil {
		fmt.Fprintf(w, "{\"error\": %q}\n", err.Error())
	}
}

type outWithHTTPStatus struct {
	status int
	output any
}

func (o outWithHTTPStatus) HTTPStatus() int {
	return o.status
}

func (o outWithHTTPStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.output)
}

// OutputWithStatus returns a value with a MarshalJSON that returns the same
// JSON encoding as the original, and with an embedded HTTP status that is
// returned to the client if this value is returned by a handler.
func OutputWithStatus(status int, out any) any {
	return outWithHTTPStatus{
		status: status,
		output: out,
	}
}
