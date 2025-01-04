package api

import (
	"net/http"
	"testing"
)

func TestNewServer(t *testing.T) {
	s := NewServer()
	if s == nil {
		t.Fatal("NewServer() returned nil")
	}
}

func TestHandler(t *testing.T) {
	shouldPanic := func(h any) {
		defer func() {
			if recover() == nil {
				t.Errorf("Handle() did not panic when handler's type is %T", h)
			}
		}()
		_ = Handler(h)
	}
	shouldNotPanic := func(h any) {
		defer func() {
			if x := recover(); x != nil {
				t.Errorf("Handle() panic'ed with type %T when it should not have: %v", h, x)
			}
		}()
		_ = Handler(h)
	}

	// The second argument to Handle (the handler) should be a function:
	shouldPanic(3)
	shouldPanic("")
	// Function must not be nil:
	var f func(*http.Request) (any, error)
	shouldPanic(f)
	// If not an ordinary HYP handler, the function should have 1 or 2 input arguments:
	shouldPanic(func() {})
	shouldPanic(func() (string, error) { return "", nil })
	shouldPanic(func(*http.Request, int, int) {})
	shouldPanic(func(*http.Request, int, int) (string, error) { return "", nil })
	// The first argument must be a *http.Request:
	shouldPanic(func(int) (string, error) { return "", nil })
	shouldPanic(func(string) (string, error) { return "", nil })
	// There must be 2 return values:
	shouldPanic(func(*http.Request) int { return 0 })
	shouldPanic(func(*http.Request, int) int { return 0 })
	shouldPanic(func(*http.Request) (int, error, int) { return 0, nil, 0 })
	shouldPanic(func(*http.Request, int) (int, error, int) { return 0, nil, 0 })
	// Second return value must be error:
	shouldPanic(func(*http.Request) (int, int) { return 0, 0 })
	shouldPanic(func(*http.Request, int) (int, int) { return 0, 0 })
	shouldPanic(func(*http.Request) (int, string) { return 0, "" })
	shouldPanic(func(*http.Request, int) (int, string) { return 0, "" })
	shouldPanic(func(*http.Request) (int, any) { return 0, nil })
	shouldPanic(func(*http.Request, int) (int, any) { return 0, nil })

	// These should not panic:
	shouldNotPanic(func(w http.ResponseWriter, r *http.Request) {}) // ordinary HTTP handler
	shouldNotPanic(func(*http.Request, any) (any, error) { return nil, nil })
	shouldNotPanic(func(*http.Request) (any, error) { return nil, nil })
}
