package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"golang.org/x/net/websocket"
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

// Serve accepts incoming connections on the specified address(es)
// and handles each connection in a goroutine.
//
// The addresses can have the form "network!addr" or just "addr",
// in which case the network is inferred
// ("unix" if the addr is a filename beginning with "/",
// or "tcp" if the addr is "host:port").
//
// Serve always returns a non-nil error.
func (s *Server) Serve(addrs ...string) error {
	if len(addrs) == 0 {
		return errors.New("Serve: no addresses to listen for connections")
	}
	var listeners []net.Listener
	errs := make(chan error)
	for _, ad := range addrs {
		network, addr, found := strings.Cut(ad, "!")
		if !found {
			if strings.HasPrefix(ad, "/") {
				network = "unix"
				addr = ad
			} else if strings.Contains(ad, ":") {
				network = "tcp"
				addr = ad
			} else {
				for _, l := range listeners {
					l.Close()
				}
				return errors.New("Serve: " + ad + ": unrecognized address")
			}
		}

		l, err := net.Listen(network, addr)
		if err != nil {
			for _, l = range listeners {
				l.Close()
			}
			return err
		}
		listeners = append(listeners, l)
		go func() {
			errs <- http.Serve(l, s)
		}()
	}
	err := <-errs
	for _, l := range listeners {
		l.Close()
	}
	return err
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

func checkPermFuncs(r *Request, permFuncs ...func(*Request) bool) bool {
	// if there are permFuncs, at least one of them must succeed
	if len(permFuncs) > 0 {
		for _, p := range permFuncs {
			if p(r) {
				return true
			}
		}
		return false
	}
	return true
}

// handleWithPerm is a wrapper that executes the provided handler unless all the
// permFuncs fail
func handleWithPerm(handler http.Handler, permFuncs ...func(*Request) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := &Request{r}

		if !checkPermFuncs(req, permFuncs...) {
			httpCodeError(w, http.StatusUnauthorized, "permission denied")
			return
		}

		handler.ServeHTTP(w, r)
	})
}

// Handle registers a handler for one pattern in the server.
//
// The function to be called when the server receives
// a petition matching the pattern will be Handler(handler, permFuncs...)
func (s *Server) Handle(pattern string, handler any, permFuncs ...func(*Request) bool) {
	if s == nil {
		panic("api.Handle: called with nil Server")
	}
	checkHandler(handler)
	s.patterns = append(s.patterns, pattern)
	s.mux.Handle(pattern, Handler(handler, permFuncs...))
	if s.debug {
		log.Printf("Added new handler: pattern=%q func=%T", pattern, handler)
	}
}

// Handler returns a http.Handler from a handler function.
//
// handler must be a function with one of these signatures:
//   - http.Handler
//   - func (http.ResponseWriter, *http.Request)
//   - func [Input, Output any] (*Request, Input) (Output, error)
//   - func [Output any] (*Request) (Output, error)
//
// If there are permFuncs, at least one of them must succeed.
//
// If the error returned by the function implements HTTPStatus,
// it is used as the HTTP Status code to be returned.
func Handler(handler any, permFuncs ...func(*Request) bool) http.Handler {
	checkHandler(handler)
	if h, ok := handler.(http.Handler); ok {
		return handleWithPerm(h, permFuncs...)
	}
	if f, ok := handler.(func(http.ResponseWriter, *http.Request)); ok {
		return handleWithPerm(http.HandlerFunc(f), permFuncs...)
	}
	t := reflect.TypeOf(handler)
	v := reflect.ValueOf(handler)
	nargs := t.NumIn()
	var tinput reflect.Type
	if nargs == 2 {
		tinput = t.In(1)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := &Request{r}
		if !checkPermFuncs(req, permFuncs...) {
			httpCodeError(w, http.StatusUnauthorized, "permission denied")
			return
		}
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
			input := reflect.New(tinput).Interface()
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
}

// Conn represents a Websocket connection.
type Conn struct {
	conn *websocket.Conn
}

// Read implements the io.Reader interface: it reads data of a frame from
// the WebSocket connection. if msg is not large enough for the frame data,
// it fills the msg and next Read will read the rest of the frame data.
// it reads Text frame or Binary frame.
func (ws *Conn) Read(msg []byte) (n int, err error) {
	return ws.conn.Read(msg)
}

// Write implements the io.Writer interface: it writes data as a frame to the
// WebSocket connection.
func (ws *Conn) Write(msg []byte) (n int, err error) {
	return ws.conn.Write(msg)
}

// HandlerWS returns a handler that tries to establish a Websocket connection,
// and calls handlerWS on success.  If it does not success, and handlerOther
// is not nil, it uses that other handler.
func HandlerWS(handler func(*Request, *Conn), handlerOther any) http.Handler {
	if handlerOther != nil {
		checkHandler(handlerOther)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Connection") != "Upgrade" || r.Header.Get("Upgrade") != "websocket" {
			if handlerOther != nil {
				Handler(handlerOther).ServeHTTP(w, r)
				return
			}
			http.Error(w, "Bad Request: needs websocket connection", http.StatusBadRequest)
			return
		}
		h := websocket.Server{Handler: func(ws *websocket.Conn) {
			conn := &Conn{ws}
			req := &Request{r}
			handler(req, conn)
		}}
		h.ServeHTTP(w, r)
	})
}
