package api

import (
	"errors"
	"log"
	"net/http"
	"reflect"
)

type Server struct {
}

type Request struct {
	http.Request
}

func NewServer() *Server {
	return nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("new request: %v", r.URL)
}

// Handle registers a handle in the server.
//
// fun must be a function with one of these signatures:
//   - func [Input, Output any] (*Request, Input) (Output, error)
//   - func [Output any] (*Request) (Output, error)
func (s *Server) Handle(pattern string, fun any, permFuncs ...func(*Request) bool) {
	t := reflect.TypeOf(fun)
	if t == nil || t.Kind() != reflect.Func {
		panic("api.Handle: Second argument must be a function")
	}
	if t.NumIn() < 1 || t.NumIn() > 2 {
		panic("api.Handle: function must have 1 or 2 arguments")
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
}
