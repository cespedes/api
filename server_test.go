package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewServer(t *testing.T) {
	s := NewServer()
	if s == nil {
		t.Fatal("NewServer() returned nil")
	}
}

func TestCheckHandler(t *testing.T) {
	s := NewServer()
	if s == nil {
		t.Fatal("NewServer() returned nil")
	}
	shouldPanic := func(h any) {
		defer func() {
			if recover() == nil {
				t.Errorf("Handle() did not panic when handler's type is %T", h)
			}
		}()
		_ = s.Handler(h)
	}
	shouldNotPanic := func(h any) {
		defer func() {
			if x := recover(); x != nil {
				t.Errorf("Handle() panic'ed with type %T when it should not have: %v", h, x)
			}
		}()
		_ = s.Handler(h)
	}

	// The second argument to Handle (the handler) should be a function:
	shouldPanic(nil)
	shouldPanic(3)
	shouldPanic("")
	// Function must not be nil:
	var f func(*http.Request) (any, error)
	shouldPanic(f)
	// If not an ordinary HTTP handler, the function should have 1 or 2 input arguments:
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
	shouldPanic(func(*http.Request) (int, int, error) { return 0, 0, nil })
	shouldPanic(func(*http.Request, int) (int, int, error) { return 0, 0, nil })
	// Second return value must be error:
	shouldPanic(func(*http.Request) (int, int) { return 0, 0 })
	shouldPanic(func(*http.Request, int) (int, int) { return 0, 0 })
	shouldPanic(func(*http.Request) (int, string) { return 0, "" })
	shouldPanic(func(*http.Request, int) (int, string) { return 0, "" })
	shouldPanic(func(*http.Request) (int, any) { return 0, nil })
	shouldPanic(func(*http.Request, int) (int, any) { return 0, nil })

	// These should not panic:
	shouldNotPanic(http.NotFoundHandler())                          // ordinary HTTP handler
	shouldNotPanic(func(w http.ResponseWriter, r *http.Request) {}) // ordinary HTTP handler func
	shouldNotPanic(func(*http.Request, any) (any, error) { return nil, nil })
	shouldNotPanic(func(*http.Request) (any, error) { return nil, nil })
}

func TestInputFields(t *testing.T) {
	s := NewServer()
	s.Handle("/foo", func(r *http.Request, in struct {
		I int
		S string
	}) (any, error) {
		m := InputFields(r)
		if in.I != int(m["i"].(float64)) {
			t.Fatalf("in.I=%d m[i]=%v\n", in.I, m["i"])
		}
		if in.S != m["s"] {
			t.Fatalf("in.S=%s m[s]=%v\n", in.S, m["s"])
		}
		return nil, nil
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/foo", bytes.NewReader([]byte(`{"i":23,"s":"string"}`)))
	s.ServeHTTP(w, r)
}

func TestServer(t *testing.T) {
	s := NewServer()
	s.Handle("/foo", func(r *http.Request) (any, error) {
		t.Fatal("function called but perm function failed")
		return nil, nil
	}, func(r *http.Request) bool {
		return false
	})
	bar := false
	s.Handle("/bar", func(r *http.Request) (any, error) {
		bar = true
		return nil, nil
	}, func(r *http.Request) bool {
		return true
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/foo", nil)
	s.ServeHTTP(w, r)

	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/bar", nil)
	s.ServeHTTP(w, r)
	time.Sleep(time.Second)
	if !bar {
		t.Fatal("function not called when perm function succeeded")
	}

	s.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bar = false
	}))
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/foo", nil)
	s.ServeHTTP(w, r)
	if bar {
		t.Fatal("denyHandler was not called")
	}
}
