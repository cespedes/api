package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"reflect"
	"sync"
)

// Server is an HTTP request multiplexer.
type Server struct {
	debug       bool
	mux         *http.ServeMux
	patterns    []string
	values      map[string]any // to be added to all the requests
	middlewares []func(http.Handler) http.Handler
	once        sync.Once
	handler     http.Handler
}

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
	req := s.newRequest(r)
	s.once.Do(func() {
		s.handler = s.mux
		for i := len(s.middlewares) - 1; i >= 0; i-- {
			s.handler = s.middlewares[i](s.handler)
		}
	})
	s.handler.ServeHTTP(w, req.Request)
}

// AddMiddleware adds a new middleware to the Server.
// This should only be called before the first call to ServeHTTP.
func (s *Server) AddMiddleware(f func(next http.Handler) http.Handler) {
	s.middlewares = append(s.middlewares, f)
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

// Get retrieves a value from a given key in this Server.
func (s *Server) Get(key string) any {
	if s.values == nil {
		return nil
	}
	return s.values[key]
}

// Request encapsulates a *http.Request to be able to use the Get and Set methods.
type Request struct {
	*http.Request
}

// newRequest initializes a Request, adding the values previously set in the Server.
func (s *Server) newRequest(r *http.Request) *Request {
	req := Request{
		Request: r,
	}
	for key, val := range s.values {
		req.Set(key, val)
	}
	return &req
}

type contextServerKey struct{}

// Set assigns a value to a given key for this Request.
// Calls to Request.Set must not be concurrent.
func (r *Request) Set(key string, value any) {
	m, ok := r.Request.Context().Value(contextServerKey{}).(map[string]any)
	if !ok {
		m = make(map[string]any)
	}
	m[key] = value
	r.Request = r.Request.WithContext(context.WithValue(r.Request.Context(), contextServerKey{}, m))
}

// Get retrieves a value from a given key in this Request.
func (r *Request) Get(key string) any {
	c := r.Request.Context()
	m, ok := c.Value(contextServerKey{}).(map[string]any)
	if !ok {
		return nil
	}
	return m[key]
}

// checkHandler panics if handler is not valid.
//
// handler must be not null, and one of:
//   - http.Handler
//   - func (http.ResponseWriter, *http.Request)
//   - func [Input, Output any] (*Request, Input) (Output, error)
//   - func [Output any] (*Request) (Output, error)
func checkHandler(handler any) {
	if handler == nil {
		panic("error: nil handler")
	}
	if _, ok := handler.(http.Handler); ok {
		return
	}
	t := reflect.TypeOf(handler)
	if t == nil || t.Kind() != reflect.Func {
		panic("handler must be a function or a http.Handler")
	}
	if t.NumIn() < 1 || t.NumIn() > 2 {
		panic("handler function must have 1 or 2 arguments")
	}
	v := reflect.ValueOf(handler)
	if v.IsZero() {
		panic("handler must be a non-nil function")
	}
	if _, ok := handler.(func(http.ResponseWriter, *http.Request)); ok {
		return
	}
	if t.In(0) != reflect.TypeOf(&Request{}) {
		panic("handler: first argument of function must have type *api.Request")
	}
	if t.NumOut() != 2 {
		panic("handler: function must have 2 return values")
	}
	if t.Out(1) != reflect.TypeOf(errors.New).Out(0) {
		panic("handler: second return value of function must have type error")
	}
}

// handleWithPerm is a wrapper that executes the provided handler unless all the
// peroFuncs fail
func handleWithPerm(handler http.Handler, permFuncs ...func(*Request) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := &Request{r}

		// if there are permFuncs, at least one of them must succeed
		if len(permFuncs) > 0 {
			allowed := false
			for _, p := range permFuncs {
				if p(req) {
					allowed = true
					break
				}
			}
			if !allowed {
				httpCodeError(w, http.StatusUnauthorized, "permission denied")
				return
			}
		}

		handler.ServeHTTP(w, r)
	})
}

// Handle registers a handle in the server.
//
// handler must be a function with one of these signatures:
//   - http.Handler
//   - func (http.ResponseWriter, *http.Request)
//   - func [Input, Output any] (*Request, Input) (Output, error)
//   - func [Output any] (*Request) (Output, error)
//
// If there are permFuncs, at least one of them must succeed.
func (s *Server) Handle(pattern string, handler any, permFuncs ...func(*Request) bool) {
	if s == nil {
		panic("api.Handle: called with nil Server")
	}
	s.patterns = append(s.patterns, pattern)
	checkHandler(handler)
	if h, ok := handler.(http.Handler); ok {
		if s.debug {
			log.Printf("Added new handler: pattern=%q (%T)", pattern, handler)
		}
		s.mux.Handle(pattern, handleWithPerm(h, permFuncs...))
		return
	}
	if f, ok := handler.(func(http.ResponseWriter, *http.Request)); ok {
		if s.debug {
			log.Printf("Added new handler: pattern=%q (%T)", pattern, handler)
		}
		s.mux.Handle(pattern, handleWithPerm(http.HandlerFunc(f), permFuncs...))
		return
	}
	t := reflect.TypeOf(handler)
	v := reflect.ValueOf(handler)
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
		req := &Request{r}
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
