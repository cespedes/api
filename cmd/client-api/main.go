package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/tidwall/pretty"
	"golang.org/x/net/websocket"
	"golang.org/x/term"
)

func main() {
	err := run(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err.Error())
		os.Exit(1)
	}
}

const (
	defaultHeaderToken = "Authorization"
	defaultTokenPrefix = "Bearer"
)

type Client struct {
	// General configuration
	name            string // name of this service (ie, "gitlab", "hass"...)
	apiEndPoint     string // base URL of the API server (ie, "https://gitlab.com/api/v4")
	apiToken        string // secret key or token to access the API
	headerToken     string // What header should we use to send the token (eg, "Authorization")
	tokenPrefix     string // What to send before the token (eg, "Bearer", "Basic"...)
	paramToken      string // What query parameter should we use to send the token (eg, "private_token")
	unixSocket      string
	websocketOrigin string

	// What to do
	method   string // GET, POST, PUT, DELETE or WS (for websocket)
	endpoint string
}

func run(args []string) error {
	c, err := NewClient(args)
	if err != nil {
		return err
	}

	switch c.method {
	case "WS":
		err = c.WS(c.endpoint)
	default:
		err = c.Request(c.method, c.endpoint)
	}
	return err
}

func NewClient(args []string) (*Client, error) {
	c := &Client{}

	flags := flag.NewFlagSet(args[0], flag.ExitOnError)

	flags.StringVar(&c.name, "name", "", "name of this service")
	flags.StringVar(&c.apiEndPoint, "api", "", "API URL")
	flags.StringVar(&c.apiToken, "token", "", "API token")
	flags.StringVar(&c.tokenPrefix, "token-prefix", "Bearer", "word to send in header before the token")
	flags.StringVar(&c.headerToken, "header", "Authorization", "header to use to send the token")

	err := flags.Parse(args[1:])
	if err != nil {
		return nil, err
	}

	if c.name == "" {
		base := path.Base(args[0])
		if strings.HasSuffix(base, "-api") || strings.HasSuffix(base, "-api.exe") {
			i := strings.LastIndex(base, "-api")
			c.name = base[:i]
		}
	}

	// I will now get default values from filesystem and environment variables

	if c.apiEndPoint == "" {
		return c, fmt.Errorf("missing API URL")
	}

	if len(flags.Args()) != 2 {
		return c, fmt.Errorf("missing method and endpoint")
	}
	c.method = flags.Arg(0)
	c.endpoint = flags.Arg(1)

	if flagIsSet(flags, "header") {
		fmt.Println("XXX flag header is set")
	}
	return c, nil
}

func flagIsSet(fs *flag.FlagSet, name string) bool {
	isSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			isSet = true
		}
	})
	return isSet
}

func (c *Client) urlAndHeader(URL string, header http.Header) (*url.URL, error) {
	// We use this instead of url.JoinPath because the latter removes possible query parameters
	u, err := url.Parse(strings.TrimSuffix(c.apiEndPoint, "/") + "/" + strings.TrimPrefix(URL, "/"))
	if err != nil {
		return nil, err
	}
	if c.apiToken != "" && c.paramToken != "" {
		v, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return nil, err
		}
		v.Add(c.paramToken, c.apiToken)
		u.RawQuery = v.Encode()
	}

	if c.apiToken != "" && c.headerToken != "" {
		token := c.apiToken
		if c.tokenPrefix != "" {
			token = c.tokenPrefix + " " + token
		}
		header.Set(c.headerToken, token)
	}
	return u, nil
}

func (c *Client) WS(endpoint string) error {
	origin := "http://localhost/"
	if c.websocketOrigin != "" {
		origin = c.websocketOrigin
	}

	header := make(http.Header)
	u, err := c.urlAndHeader(endpoint, header)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	config, err := websocket.NewConfig(u.String(), origin)
	if err != nil {
		return err
	}
	config.Header = header

	ws, err := websocket.DialConfig(config)
	if err != nil {
		return err
	}

	// with websockets we always output the messages without modification
	for {
		var message string
		err = websocket.Message.Receive(ws, &message)
		if err != nil {
			break
		}
		fmt.Println(message)
	}
	return nil
}

func (c *Client) Request(method, endpoint string) error {
	header := make(http.Header)
	u, err := c.urlAndHeader(endpoint, header)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header = header

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
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if term.IsTerminal(1) {
		p := pretty.Pretty(b)
		b = pretty.Color(p, nil)
	}
	fmt.Print(string(b))

	return nil
}
