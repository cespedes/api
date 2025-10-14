package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime/debug"
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
	defaultEmptyArg    = "c7fe09e3-23a2-4b76-a1c6-c23bc667f41c"
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
	stdin           bool // read request body from stdin

	// What to do
	method   string // GET, POST, PUT, DELETE or WS (for websocket)
	endpoint string
	body     string

	// Other things
	verbose bool
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
		err = c.Request(c.method, c.endpoint, c.body)
	}
	return err
}

func NewClient(args []string) (*Client, error) {
	c := &Client{}

	flags := flag.NewFlagSet(args[0], flag.ExitOnError)

	params := []struct {
		Name        string
		Addr        *string
		Default     string
		Description string
	}{
		{
			Name:        "api",
			Addr:        &c.apiEndPoint,
			Default:     "",
			Description: "API URL",
		},
		{
			Name:        "token",
			Addr:        &c.apiToken,
			Default:     "",
			Description: "API key or token",
		},
		{
			Name:        "header",
			Addr:        &c.headerToken,
			Default:     "",
			Description: "header to use to send the token (default \"Authorization\")",
		},
		{
			Name:        "token-prefix",
			Addr:        &c.tokenPrefix,
			Default:     "",
			Description: "word to send in header before the token (default \"Bearer\")",
		},
		{
			Name:        "token-param",
			Addr:        &c.paramToken,
			Default:     "",
			Description: "query parameter to use to send the token (eg, \"private_token\")",
		},
	}

	flags.StringVar(&c.name, "name", "", "name of this service")
	flags.BoolVar(&c.verbose, "verbose", false, "show headers sent and received")
	flags.BoolVar(&c.stdin, "stdin", false, "read request body from stdin")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: %s [options] METHOD /endpoint [body]\n", args[0])
		flags.PrintDefaults()
	}

	for _, p := range params {
		flags.StringVar(p.Addr, p.Name, p.Default, p.Description)
	}

	err := flags.Parse(args[1:])
	if err != nil {
		return nil, err
	}

	for _, p := range params {
		if !flagIsSet(flags, p.Name) {
			*p.Addr = defaultEmptyArg
		}
	}

	if c.name == "" {
		base := path.Base(args[0])
		if strings.HasSuffix(base, "-api") || strings.HasSuffix(base, "-api.exe") {
			i := strings.LastIndex(base, "-api")
			c.name = base[:i]
		}
	}

	// I will now get default values from filesystem and environment variables
	// Order of precedence, from most preferred to least preferred:
	//   - $NAME_API and $NAME_TOKEN
	//   - $HOME/.name-api and $HOME/.name-token
	//   - $HOME/.name-api.json
	//   - $HOME/.name-api.conf
	//   - /etc/name-api and /etc/name-token
	//   - /etc/name-api.json
	//   - /etc/name-api.conf
	if c.name != "" {
		readConfigFromFile := func(fname string) {
			b, err := os.ReadFile(fname)
			if err != nil {
				return
			}
			var m map[string]any
			err = json.Unmarshal(b, &m)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error unmarshaling %s: %v\n", fname, err)
				return
			}
			for _, p := range params {
				x := m[p.Name]
				if s, ok := x.(string); ok && *p.Addr == defaultEmptyArg {
					*p.Addr = s
				}
			}
		}
		// $NAME_API and $NAME_TOKEN
		NAME := strings.ToUpper(c.name)
		if endpoint, ok := os.LookupEnv(fmt.Sprintf("%s_API", NAME)); ok {
			if c.apiEndPoint == defaultEmptyArg {
				c.apiEndPoint = endpoint
			}
		}
		if token, ok := os.LookupEnv(fmt.Sprintf("%s_TOKEN", NAME)); ok {
			if c.apiToken == defaultEmptyArg {
				c.apiToken = token
			}
		}
		if home, ok := os.LookupEnv("HOME"); ok {
			// $HOME/.name-api and $HOME/.name-token
			if endpoint, err := os.ReadFile(fmt.Sprintf("%s/.%s-api", home, c.name)); err == nil {
				if c.apiEndPoint == defaultEmptyArg {
					c.apiEndPoint = strings.TrimSpace(string(endpoint))
				}
			}
			if token, err := os.ReadFile(fmt.Sprintf("%s/.%s-token", home, c.name)); err == nil {
				if c.apiToken == defaultEmptyArg {
					c.apiToken = strings.TrimSpace(string(token))
				}
			}
			// $HOME/.name-api.json and $HOME/.name-api.conf
			readConfigFromFile(fmt.Sprintf("%s/.%s-api.json", home, c.name))
			readConfigFromFile(fmt.Sprintf("%s/.%s-api.conf", home, c.name))
		}
		// /etc/name-api and /etc/name-token
		if endpoint, err := os.ReadFile(fmt.Sprintf("/etc/%s-api", c.name)); err == nil {
			if c.apiEndPoint == defaultEmptyArg {
				c.apiEndPoint = strings.TrimSpace(string(endpoint))
			}
		}
		if token, err := os.ReadFile(fmt.Sprintf("/etc/%s-token", c.name)); err == nil {
			if c.apiToken == defaultEmptyArg {
				c.apiToken = strings.TrimSpace(string(token))
			}
		}
		// /etc/name-api.json and /etc/name-api.conf
		readConfigFromFile(fmt.Sprintf("/etc/.%s-api.json", c.name))
		readConfigFromFile(fmt.Sprintf("/etc/.%s-api.conf", c.name))
	}

	if c.headerToken == defaultEmptyArg && c.paramToken == defaultEmptyArg {
		c.headerToken = defaultHeaderToken
		if c.tokenPrefix == defaultEmptyArg {
			c.tokenPrefix = defaultTokenPrefix
		}
	}
	for _, p := range params {
		if *p.Addr == defaultEmptyArg {
			*p.Addr = ""
		}
	}

	if c.apiEndPoint == "" && c.name == "" {
		return c, fmt.Errorf("cannot find API service")
	}

	if c.apiEndPoint == "" {
		return c, fmt.Errorf("cannot find API URL in args, $%s_API, ~/.%s-api or /etc/%s-api",
			strings.ToUpper(c.name), c.name, c.name)
	}

	if len(flags.Args()) < 2 || len(flags.Args()) > 3 {
		fmt.Fprintln(flags.Output(), "Error: incorrect number of parameters")
		flags.Usage()
		os.Exit(1)
	}
	c.method = flags.Arg(0)
	c.endpoint = flags.Arg(1)
	c.body = flags.Arg(2)
	if c.stdin {
		if c.body != "" {
			fmt.Fprintln(flags.Output(), "Error: option '-s' cannot be used with 3 arguments.")
			fmt.Fprintf(flags.Output(), "Try '%s --help' for more information.\n", args[0])
			os.Exit(1)
		}
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return c, err
		}
		c.body = string(b)
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

	// send User-Agent:
	ua := "client-api"
	if c.name != "" {
		ua = c.name + "-api"
	}
	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		ua += "/" + buildInfo.Main.Version
	}
	ua += " (https://github.com/cespedes/api)"
	header.Set("User-Agent", ua)

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

func (c *Client) Request(method, endpoint string, body string) error {
	header := make(http.Header)
	u, err := c.urlAndHeader(endpoint, header)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, u.String(), strings.NewReader(body))
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

	if c.verbose {
		fmt.Printf("> %s %s\n", method, u.String())
		for k, v := range header {
			for _, v2 := range v {
				fmt.Printf("> %s: %s\n", k, v2)
			}
		}
		fmt.Println()
		fmt.Printf("%s %s\n", resp.Proto, resp.Status)
		for k, v := range resp.Header {
			for _, v2 := range v {
				fmt.Printf("%s: %s\n", k, v2)
			}
		}
		fmt.Println()
	}

	if term.IsTerminal(1) {
		contentType := resp.Header.Get("Content-Type")
		contentType, _, _ = strings.Cut(contentType, ";")
		contentType = strings.ToLower(strings.TrimSpace(contentType))
		if contentType == "application/json" {
			p := pretty.Pretty(b)
			b = pretty.Color(p, nil)
		}
	}
	fmt.Print(string(b))

	return nil
}
