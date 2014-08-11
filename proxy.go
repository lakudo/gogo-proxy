package proxy

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/koding/websocketproxy"
)

type ProxyOptions struct {
	// Number of times a request should be tried
	Retries int

	// Period to wait between retries
	Period time.Duration

	// Returns a url that we should proxy to for a given request
	Balancer func(req *http.Request) (string, error)

	// A static backend to route to
	Backend string
}

type Proxy struct {
	*ProxyOptions

	// Http proxy
	httpProxy http.Handler

	// Websocket proxy
	websocketProxy http.Handler
}

// New returns a new Proxy instance based on the provided ProxyOptions
// either 'Backend' (static) or 'Balancer' must be provided
func New(opts ProxyOptions) (*Proxy, error) {
	// Validate Balancer and Backend options
	if opts.Balancer == nil {
		if opts.Backend == "" {
			return nil, errors.New("Please provide a Backend or a Balancer")
		} else if err := validateUrl(normalizeUrl(opts.Backend)); err != nil {
			return nil, err
		} else {
			// Normalize backend's url
			opts.Backend = normalizeUrl(opts.Backend)
		}
	}

	// Default for Retries
	if opts.Retries == 0 {
		opts.Retries = 1
	}

	// Default for Period
	if opts.Period == 0 {
		opts.Period = 100 * time.Millisecond
	}

	p := &Proxy{
		ProxyOptions: &opts,
	}

	return p.init(), nil
}

// ServeHTTP allows us to comply to the http.Handler interface
func (p *Proxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if isWebsocket(req) {
		// we don't use https explicitly, ssl termination is done here
		req.URL.Scheme = "ws"
		p.websocketProxy.ServeHTTP(rw, req)
		return
	}

	p.httpProxy.ServeHTTP(rw, req)
}

// init sets up proxies and other stuff based on options
func (p *Proxy) init() *Proxy {
	// Setup http proxy
	p.httpProxy = &httputil.ReverseProxy{
		Director: p.director,
	}

	// Setup websocket proxy
	p.websocketProxy = &websocketproxy.WebsocketProxy{
		Backend: func(req *http.Request) *url.URL {
			url, _ := p.backend(req)
			return url
		},
		Upgrader: &websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}

	return p
}

// director rewrites a http.Request to route to the correct host
func (p *Proxy) director(req *http.Request) {
	url, err := p.backend(req)
	if url == nil || err != nil {
		return
	}

	// Rewrite outgoing request url
	req.URL.Scheme = url.Scheme
	req.URL.Host = url.Host
	req.URL.Path = url.Path

	req.Host = url.Host
}

// backend parses the result of getBackend and ensures it's validity
func (p *Proxy) backend(req *http.Request) (*url.URL, error) {
	rawurl, err := p.getBackend(req)
	if err != nil {
		return nil, err
	}

	// Normalize URL
	backendUrl := normalizeUrl(rawurl)

	if err := validateUrl(backendUrl); err != nil {
		return nil, err
	}

	return url.Parse(backendUrl)
}

// getBackend gets the backend selected by the balancer or the static one set by the 'Backend' attribute
func (p *Proxy) getBackend(req *http.Request) (string, error) {
	if p.Balancer == nil && p.Backend != "" {
		return p.Backend, nil
	}
	return p.Balancer(req)
}

// validateUrl generates an error if the the url isn't absolute or valid
func validateUrl(rawurl string) error {
	parsed, err := url.Parse(rawurl)
	if err != nil {
		return err
	}

	// Ensure url is absolute
	if !parsed.IsAbs() {
		return errors.New("Proxy must only proxy to absolute URLs")
	}

	// All is good
	return nil
}

// normalizeUrl try's to add a scheme to a url if doesn't any
func normalizeUrl(rawurl string) string {
	// default "://" to "http://"
	if strings.HasPrefix(rawurl, "://") {
		rawurl = strings.Replace(rawurl, "://", "http://", 1)
	}

	parsed, err := url.Parse(rawurl)
	if err != nil {
		return rawurl
	}

	// Cleanup or default scheme to http
	newScheme := httpScheme(parsed.Scheme)
	if newScheme != parsed.Scheme {
		// Use new scheme
		parsed.Scheme = newScheme

		// We need to reparse the URL because now that there is a prefix
		// the "Host" and "Path" fields are most likely going to change
		parsed, err = url.Parse(parsed.String())
		if err != nil {
			return rawurl
		}
	}

	// Default path
	if parsed.Path == "" {
		parsed.Path = "/"
	}

	// Return URL string
	return parsed.String()
}

// websocketScheme picks a suitable websocket scheme
func websocketScheme(scheme string) string {
	switch scheme {
		case "http":
			return "ws"
		case "https":
			return "wss"
		case "ws":
		case "wss":
			return scheme
	}
	// Default
	return "ws"
}

// httpScheme picks a suitable http scheme
func httpScheme(scheme string) string {
	switch scheme {
		case "ws":
			return "http"
		case "wss":
			return "https"
		case "http":
		case "https":
			return scheme
	}
	// Default
	return "http"
}

// isWebsocket checks wether the incoming request is a part of websocket handshake
func isWebsocket(req *http.Request) bool {
	if strings.ToLower(req.Header.Get("Upgrade")) != "websocket" ||
		!strings.Contains(strings.ToLower(req.Header.Get("Connection")), "upgrade") {
		return false
	}
	return true
}
