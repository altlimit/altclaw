//go:build integration
// +build integration

package netx

import (
"net"
"net/http"
"net/http/httptest"
"strings"
"testing"
)

// TestProxyCONNECT tests that our proxy correctly tunnels a CONNECT request.
func TestProxyCONNECT(t *testing.T) {
// Start a fake target server (echo)
target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
te("hello"))
}))
defer target.Close()

proxy, err := NewProxyServer()
if err != nil {
proxy.Close()

// Manually send a CONNECT request to our proxy
conn, err := net.Dial("tcp", proxy.Addr())
if err != nil {
conn.Close()

targetHost := strings.TrimPrefix(target.URL, "https://")
req := "CONNECT " + targetHost + " HTTP/1.1\r\nHost: " + targetHost + "\r\n\r\n"
conn.Write([]byte(req))

buf := make([]byte, 1024)
n, _ := conn.Read(buf)
response := string(buf[:n])
t.Logf("Proxy CONNECT response: %q", response)

if !strings.Contains(response, "200") {
200 in CONNECT response, got: %q", response)
}
}
