package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const (
	defaultHeaderToken = "Authorization"
	defaultTokenPrefix = "Bearer"
)

// Client is a way to connect to 3rd party API servers.
type Client struct {
	apiEndPoint           string
	apiToken              string
	headerToken           string // What header should we use to send the token (eg, "Authorization")
	tokenPrefix           string // What to send before the token (eg, "Bearer", "Basic"...)
	paramToken            string // What query parameter should we use to send the token (eg, "private_token")
	disallowUnknownFields bool
	unixSocket            string
}

// NewClient creates a new Client ready to use.
func NewClient(apiEndPoint string) *Client {
	return &Client{apiEndPoint: apiEndPoint}
}

// WithToken adds a token to a Client.
func (c *Client) WithToken(tk string) *Client {
	c2 := new(Client)
	*c2 = *c
	c2.apiToken = tk
	return c2
}

// WithHeaderToken specifies which Header line to use when sending a token.
func (c *Client) WithHeaderToken(ht string) *Client {
	c2 := new(Client)
	*c2 = *c
	c2.headerToken = ht
	return c2
}

// WithTokenPrefix adds an optional prefix to the token in the Header line.
func (c *Client) WithTokenPrefix(tp string) *Client {
	c2 := new(Client)
	*c2 = *c
	c2.tokenPrefix = tp
	return c2
}

// WithParamToken specifies which query parameter to use when sending a token.
func (c *Client) WithParamToken(pt string) *Client {
	c2 := new(Client)
	*c2 = *c
	c2.paramToken = pt
	return c2
}

// DisallowUnknownFields causes the JSON decoder to return an error when the
// destination is a struct and the input contains object keys which do not
// match any non-ignored, exported fields in the destination.
func (c *Client) DisallowUnknownFields() *Client {
	c2 := new(Client)
	*c2 = *c
	c2.disallowUnknownFields = true
	return c2
}

// WithUnixSocket causes the client to connect through this Unix domain socket,
// instead of using the network.
func (c *Client) WithUnixSocket(socket string) *Client {
	c2 := new(Client)
	*c2 = *c
	c2.unixSocket = socket
	return c2
}

// Request makes a HTTP request to the API.
//
// If data is a []byte, it will be sent as-is; otherwise, it will be encoded using JSON.
//
// If dest is a pointer to a []byte, it will receive the output as-is; otherwise,
// the output will be JSON-decoded.
func (c *Client) Request(method, URL string, data any, dest any) error {
	var err error
	var body io.Reader

	if data != nil {
		var b []byte
		switch d := data.(type) {
		case []byte:
			b = d
		default:
			b, err = json.Marshal(data)
			if err != nil {
				return err
			}
		}
		body = bytes.NewBuffer(b)
	}

	// make headerToken and tokenPrefix the default values if needed, but only for this call.
	headerToken, tokenPrefix := c.headerToken, c.tokenPrefix
	if c.apiToken != "" && headerToken == "" && c.paramToken == "" {
		headerToken = defaultHeaderToken
		if tokenPrefix == "" {
			tokenPrefix = defaultTokenPrefix
		}
	}

	// We use this instead of url.JoinPath because the latter removes possible query parameters
	u, err := url.Parse(strings.TrimSuffix(c.apiEndPoint, "/") + "/" + strings.TrimPrefix(URL, "/"))
	if err != nil {
		return err
	}
	if c.apiToken != "" && c.paramToken != "" {
		v, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return err
		}
		v.Add(c.paramToken, c.apiToken)
		u.RawQuery = v.Encode()
	}

	req, err := http.NewRequest(method, u.String(), body)
	if err != nil {
		return err
	}
	if c.apiToken != "" && headerToken != "" {
		token := c.apiToken
		if tokenPrefix != "" {
			token = tokenPrefix + " " + token
		}
		req.Header.Set(headerToken, token)
	}
	client := &http.Client{}
	if c.unixSocket != "" {
		client.Transport = &http.Transport{
			Dial: func(proto, addr string) (conn net.Conn, err error) {
				return net.Dial("unix", c.unixSocket)
			},
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("api: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var foo struct {
			Error string
		}
		decoder := json.NewDecoder(resp.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&foo); err != nil {
			return fmt.Errorf("%s", resp.Status)
		}
		return fmt.Errorf("%s: %s", resp.Status, foo.Error)
	}
	if dest == nil {
		return nil
	}
	if d, ok := dest.(*[]byte); ok {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		*d = b
		return nil
	}
	decoder := json.NewDecoder(resp.Body)
	if c.disallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	return nil
}

// Get makes a HTTP GET request to the API.
func (c *Client) Get(url string, dest any) error {
	return c.Request("GET", url, nil, dest)
}

// Post makes a HTTP POST request to the API.
func (c *Client) Post(url string, data any, dest any) error {
	return c.Request("POST", url, data, dest)
}

// Put makes a HTTP PUT request to the API.
func (c *Client) Put(url string, data any, dest any) error {
	return c.Request("PUT", url, data, dest)
}

// Delete makes a HTTP DELETE request to the API.
func (c *Client) Delete(url string, dest any) error {
	return c.Request("DELETE", url, []byte(nil), dest)
}
