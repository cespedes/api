package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"reflect"
)

// Server is an HTTP request multiplexer.
type Server struct {
	debug    bool
	mux      *http.ServeMux
	patterns []string
	values   map[string]any // to be added to all the requests
}

// Set assigns a value to a given key for all the requests
// in a given server.
// Calls to Server.Set must not be concurrent.
func (s *Server) Set(key string, value any) {
	if s.values == nil {
		s.values = make(map[string]any)
	}
	s.values[key] = value
}

// Request encapsulates a *http.Request to be able to use the Get and Set methods.
type Request struct {
	server *Server
	*http.Request
}

type contextServerKey struct{}

// Set assigns a value to a given key for this Request.
func (r *Request) Set(key string, value any) {
	m, ok := r.Request.Context().Value(contextServerKey{}).(map[string]any)
	if !ok {
		m = make(map[string]any)
	}
	m[key] = value
	r.Request = r.Request.WithContext(context.WithValue(r.Request.Context(), contextServerKey{}, m))
}

// Get retrieves assigns a value from a given in this Request.
func (r *Request) Get(key string) any {
	m, ok := r.Request.Context().Value(contextServerKey{}).(map[string]any)
	if !ok {
		return nil
	}
	return m[key]
}

/*
func (r *Request) GetString(key string) string {
	s, _ := r.Get(key).(string)
	return s
}
*/

// NewServer allocates and returns a new Server.
func NewServer() *Server {
	var s Server
	s.mux = http.NewServeMux()
	s.debug = false
	return &s
}

// ServeHTTP creates a Request, runs the middleware functions,
// and dispatches the HTTP request to the correct handler from
// those registered in the server.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.debug {
		log.Printf("api.Server.ServeHTTP: new request: %v", r.URL)
	}
	req := Request{
		Request: r,
	}
	for key, val := range s.values {
		req.Set(key, val)
	}
	s.mux.ServeHTTP(w, r)
}

// Handle registers a handle in the server.
//
// handler must be a function with one of these signatures:
//   - func [Input, Output any] (*Request, Input) (Output, error)
//   - func [Output any] (*Request) (Output, error)
//   - func (http.ResponseWriter, *http.Request)
func (s *Server) Handle(pattern string, handler any, permFuncs ...func(*Request) bool) {
	if s == nil {
		panic("api.Handle: called with nil Server")
	}
	s.patterns = append(s.patterns, pattern)
	if handler == nil {
		panic("api.Handle: called with nil handler")
	}
	t := reflect.TypeOf(handler)
	if t == nil || t.Kind() != reflect.Func {
		panic("api.Handle: Second argument must be a function")
	}
	if t.NumIn() < 1 || t.NumIn() > 2 {
		panic("api.Handle: function must have 1 or 2 arguments")
	}
	v := reflect.ValueOf(handler)
	if v.IsZero() {
		panic("api.Handle: Second argument must be a non-nil function")
	}
	if f, ok := handler.(func(http.ResponseWriter, *http.Request)); ok {
		s.mux.HandleFunc(pattern, f)
		return
	}
	if t.In(0) != reflect.TypeOf(&Request{}) {
		panic("api.Handle: first argument of function must have type *api.Request")
	}
	if t.NumOut() != 2 {
		panic("api.Handle: function must have 2 return values")
	}
	if t.Out(1) != reflect.TypeOf(errors.New).Out(0) {
		panic("api.Handle: second return value of function must have type error")
	}
	nargs := t.NumIn()
	var input any
	if nargs == 2 {
		input = reflect.New(t.In(1)).Interface()
	}
	// out := t.In(1)
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if s.debug {
			log.Printf("calling handler for pattern %q", pattern)
		}
		var req *Request
		var out []reflect.Value
		if nargs == 1 {
			out = v.Call([]reflect.Value{reflect.ValueOf(req)})
		} else {
			if r.ContentLength == 0 {
				httpError(w, "no body supplied")
				return
			}
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				httpError(w, "parsing body: %w", err)
				return
			}

			out = v.Call([]reflect.Value{reflect.ValueOf(req), reflect.ValueOf(input).Elem()})
		}
		output := out[0].Interface()
		var err error
		if e := out[1].Interface(); e != nil {
			err = out[1].Interface().(error)
		}
		if s.debug {
			log.Printf("output from handler: (%v, %v)", output, err)
		}
		if err != nil {
			httpError(w, err)
			return
		}

		// if the returned type is a string, output it as a "info" message:
		if s, ok := output.(string); ok {
			httpInfo(w, s)
			return
		}

		// if the returned type is a []byte, output it directly:
		if b, ok := output.([]byte); ok {
			w.Write(b)
			return
		}
		httpJSON(w, output)
	})
	if s.debug {
		log.Printf("Added new handler: pattern=%q func=%T", pattern, handler)
	}
}

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

// Error returns an ErrHTTPStatus from another error or a printf-like string.
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
	/*
		switch {
		case ErrorIsNotFound(err):
			code = http.StatusNotFound
			err = errors.New("not found")
		}
	*/
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

// httpError sends a HTTP error as a response
func httpError(w http.ResponseWriter, f any, a ...any) {
	err := apiError(f, a...).(errHTTPStatus)
	/*
		var perr *pq.Error
		if errors.As(err, &perr) {
			w.Header().Set("X-SQL-Error", fmt.Sprintf("%s %s", perr.Code, perr.Message))
		}
	*/
	httpMessage(w, err.Status, "error", err.Error())
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
