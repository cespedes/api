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
//   - httpInfo()
//   - httpJSON()

// Dependencies:
//   - HTTPError     -> errHTTPStatus
//   - HTTPStatus    -> (none)
//   - httpError     -> httpMessage
//   - httpCodeError -> HTTPError, httpError
//   - httpInfo      -> httpMessage
//   - httpJSON      -> (none)
//   - apiError      -> errHTTPStatus, HTTPError
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

// Error returns an errHTTPStatus from another error or a printf-like string.
// The default HTTP status code is BadRequest.
func apiError(f any, a ...any) error {
	if err, ok := f.(errHTTPStatus); ok {
		return err
	}

	var err error
	if e, ok := f.(error); ok {
		err = e
	} else if s, ok := f.(string); ok {
		err = fmt.Errorf(s, a...)
	} else {
		err = errors.New(fmt.Sprint(f))
	}

	code := http.StatusBadRequest
	switch {
	case errors.Is(err, sql.ErrNoRows):
		code = http.StatusNotFound
		err = errors.New("not found")
	}
	return HTTPError(code, err)
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

// httpError sends a HTTP error as a response
func httpCodeError(w http.ResponseWriter, code int, f any, a ...any) {
	err := HTTPError(code, f, a...).(errHTTPStatus)
	httpError(w, err)
}

func httpInfo(w http.ResponseWriter, msg any) {
	httpMessage(w, http.StatusOK, "info", fmt.Sprint(msg))
}

func httpMessage(w http.ResponseWriter, code int, label string, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, "{%q: %q}\n", label, msg)
}

func httpJSON(w http.ResponseWriter, output any, codes ...int) {
	code := http.StatusOK
	if len(codes) > 0 {
		code = codes[0]
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	e := json.NewEncoder(w)
	err := e.Encode(output)
	if err != nil {
		fmt.Fprintf(w, "{\"error\": %q}\n", err.Error())
	}
}
