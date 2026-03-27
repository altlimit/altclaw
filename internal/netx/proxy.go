package netx

import (
	
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
)

// ProxyServer is a simple HTTP CONNECT proxy that enforces SSRF rules
// via SafeDialer. It is used to provide public internet access to
// isolated Docker containers.
type ProxyServer struct {
	addr   string
	server *http.Server
}

// NewProxyServer creates and starts a proxy server on a random available port.
// Binds to 127.0.0.1 to prevent network exposure.
func NewProxyServer() (*ProxyServer, error) {
	// Let the OS pick a free port — bind to loopback only
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to listen for proxy: %w", err)
	}

	p := &ProxyServer{
		addr: listener.Addr().String(),
	}

	handler := http.HandlerFunc(p.handleRequest)
	p.server = &http.Server{Handler: handler}

	go func() {
		_ = p.server.Serve(listener)
	}()

	return p, nil
}

// Addr returns the address (ip:port) the proxy is listening on.
func (p *ProxyServer) Addr() string {
	return p.addr
}

// Port returns the integer port the proxy is listening on.
func (p *ProxyServer) Port() int {
	_, port, _ := net.SplitHostPort(p.addr)
	var pNum int
	fmt.Sscanf(port, "%d", &pNum)
	return pNum
}

// Close gracefully shuts down the proxy server.
func (p *ProxyServer) Close() error {
	if p.server != nil {
		return p.server.Close()
	}
	return nil
}

func (p *ProxyServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleTunneling(w, r)
	} else {
		p.handleHTTP(w, r)
	}
}

func (p *ProxyServer) handleTunneling(w http.ResponseWriter, r *http.Request) {
	// Use the safe dialer to prevent connecting to restricted internal IPs
	slog.Debug("proxy CONNECT", "target", r.Host)
	destConn, err := SafeDialer(r.Context(), "tcp", r.Host)
	if err != nil {
		slog.Debug("proxy CONNECT denied", "target", r.Host, "err", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	// Important: close destination connection when done
	defer destConn.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, brw, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	// Important: close client connection when done
	defer clientConn.Close()

	// Send 200 OK back to let the client know the tunnel is established.
	// Use HTTP/1.1 to match standard proxy behavior (squid, nginx, etc).
	clientConn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))

	// Copy data bidirectionally between client and destination.
	// IMPORTANT: use the buffered reader from Hijack — Go's HTTP server may have
	// read-ahead buffered bytes (e.g. early TLS ClientHello) that would be lost
	// if we read from the raw net.Conn directly.
	var clientReader io.Reader = clientConn
	if brw != nil && brw.Reader.Buffered() > 0 {
		clientReader = brw.Reader
	}
	go transfer(destConn, io.NopCloser(clientReader))
	transfer(clientConn, destConn)
}

func (p *ProxyServer) handleHTTP(w http.ResponseWriter, r *http.Request) {
	// Remove connection headers
	r.Header.Del("Proxy-Connection")

	// The HTTP client needs to hit the target URL directly
	reqHost := r.URL.Host
	if reqHost == "" {
		reqHost = r.Host
	}

	destConn, err := SafeDialer(r.Context(), "tcp", r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer destConn.Close()

	if r.URL.Scheme == "" {
		r.URL.Scheme = "http"
	}
	if r.URL.Host == "" {
		r.URL.Host = r.Host
	}

	// Better to use actual roundtripper using our safe dialer instead of hijacking
	// raw connections for HTTP requests to be more standard.
	transport := &http.Transport{
		DialContext: SafeDialer,
	}

	// Create a new request based on the incoming one
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	outReq.Header = r.Header

	resp, err := transport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func transfer(destination io.WriteCloser, source io.ReadCloser) {
	defer destination.Close()
	defer source.Close()
	io.Copy(destination, source)
}
