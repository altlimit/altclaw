// Package tunnel provides a yamux-based tunnel client that connects
// an altclaw instance to the hub relay server, making it accessible
// via a public URL (e.g. <hostname>.altclaw.ai).
package tunnel

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// Registration is sent to the hub on TCP connect.
type Registration struct {
	Token    string `json:"token,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

// Assignment is the hub's response after registration.
type Assignment struct {
	Hostname string `json:"hostname,omitempty"`
	Domain   string `json:"domain,omitempty"`
	HttpPort int    `json:"http_port,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Client tunnels a local altclaw web server through the hub relay.
type Client struct {
	HubAddr  string       // TCP address of the hub relay (host:port)
	Token    string       // workspace token (empty for anonymous)
	AssignedHost string       // hub-assigned hostname to register with
	Handler  http.Handler // the altclaw web server handler

	mu       sync.Mutex
	session  *yamux.Session
	hostname string
	domain   string
}

// New creates a new tunnel client.
func New(hubAddr string, token string, hostname string, handler http.Handler) *Client {
	return &Client{
		HubAddr:  hubAddr,
		Token:    token,
		AssignedHost: hostname,
		Handler:  handler,
	}
}

// Connect dials the hub, registers, and establishes a yamux session.
// Returns the assigned full URL on success.
func (c *Client) Connect(ctx context.Context) (string, error) {
	host := c.HubAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config: &tls.Config{
			ServerName: host,
		},
	}

	conn, err := dialer.DialContext(ctx, "tcp", c.HubAddr)
	if err != nil {
		// Fallback to plain TCP for dev mode
		plainDialer := &net.Dialer{Timeout: 10 * time.Second}
		conn, err = plainDialer.DialContext(ctx, "tcp", c.HubAddr)
		if err != nil {
			return "", fmt.Errorf("dial hub: %w", err)
		}
	}

	// Send registration
	reg := Registration{
		Token:    c.Token,
		Hostname: c.AssignedHost,
	}
	if err := json.NewEncoder(conn).Encode(reg); err != nil {
		conn.Close()
		return "", fmt.Errorf("send registration: %w", err)
	}

	// Read assignment
	var assign Assignment
	if err := json.NewDecoder(conn).Decode(&assign); err != nil {
		conn.Close()
		return "", fmt.Errorf("read assignment: %w", err)
	}
	if assign.Error != "" {
		conn.Close()
		return "", fmt.Errorf("hub error: %s", assign.Error)
	}

	// Establish yamux session (altclaw acts as yamux Server — accepts streams opened by hub)
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 30 * time.Second
	cfg.ConnectionWriteTimeout = 30 * time.Second
	cfg.StreamOpenTimeout = 10 * time.Second
	cfg.LogOutput = io.Discard

	session, err := yamux.Server(conn, cfg)
	if err != nil {
		conn.Close()
		return "", fmt.Errorf("yamux session: %w", err)
	}

	c.mu.Lock()
	c.session = session
	c.hostname = assign.Hostname
	c.domain = assign.Domain
	c.mu.Unlock()

	url := assign.Hostname
	if assign.Domain != "" {
		url = assign.Hostname + "-relay." + assign.Domain
	}

	return url, nil
}

// Run serves HTTP requests from the hub through the yamux session.
// Blocks until the context is cancelled or the session dies.
func (c *Client) Run(ctx context.Context) error {
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()

	if session == nil {
		return fmt.Errorf("not connected")
	}

	ln := &yamuxListener{session: session}
	srv := &http.Server{
		Handler:      c.Handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // no write timeout for SSE
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return ctx.Err()
	}
	return err
}

// Close shuts down the yamux session.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}

// Hostname returns the assigned hostname.
func (c *Client) Hostname() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hostname
}

// Domain returns the hub's domain.
func (c *Client) Domain() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.domain
}

// FullURL returns the full tunnel URL (hostname.domain).
func (c *Client) FullURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.domain != "" {
		return c.hostname + "." + c.domain
	}
	return c.hostname
}

// RunWithReconnect runs the tunnel with automatic reconnection.
// discoverFn is called before each connection attempt to re-authorize
// the hostname and return the relay TCP address.
func (c *Client) RunWithReconnect(ctx context.Context, discoverFn func() (tcpAddr string, hostname string, err error), onConnect func(url string), onDisconnect func(err error)) {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Re-authorize hostname before each connection attempt.
		tcpAddr, hostname, err := discoverFn()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("tunnel discover failed", "error", err)
			if onDisconnect != nil {
				onDisconnect(fmt.Errorf("discover: %w", err))
			}
			time.Sleep(backoff)
			backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
			continue
		}
		c.HubAddr = tcpAddr
		c.AssignedHost = hostname

		url, err := c.Connect(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("tunnel connect failed", "error", err)
			if onDisconnect != nil {
				onDisconnect(fmt.Errorf("connect failed: %w", err))
			}
			time.Sleep(backoff)
			backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
			continue
		}

		backoff = time.Second
		if onConnect != nil {
			onConnect(url)
		}

		err = c.Run(ctx)
		if ctx.Err() != nil {
			return
		}

		c.Close()
		if onDisconnect != nil {
			onDisconnect(err)
		}

		time.Sleep(time.Second)
	}
}

// yamuxListener adapts a yamux.Session into a net.Listener.
type yamuxListener struct {
	session *yamux.Session
}

func (l *yamuxListener) Accept() (net.Conn, error) {
	return l.session.AcceptStream()
}

func (l *yamuxListener) Close() error {
	return l.session.Close()
}

func (l *yamuxListener) Addr() net.Addr {
	return l.session.LocalAddr()
}

// bufioReaderFromStream creates a bufio.Reader from a stream for HTTP response parsing.
var _ = bufio.NewReader // keep import
