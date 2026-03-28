FROM docker.io/library/golang:1.23-alpine AS build
WORKDIR /src

# Inline the proxy source — no external files needed.
# Avoid heredoc syntax (unsupported in older Podman builders).
RUN printf 'module altclaw.ai/proxy\ngo 1.23\n' > go.mod

RUN printf '%s\n' \
  'package main' \
  '' \
  'import (' \
  '	"io"' \
  '	"log"' \
  '	"net"' \
  '	"net/http"' \
  '	"os"' \
  '	"strconv"' \
  '	"strings"' \
  '	"time"' \
  ')' \
  '' \
  'var privateCIDRs = []string{' \
  '	"127.0.0.0/8","10.0.0.0/8","172.16.0.0/12","192.168.0.0/16",' \
  '	"169.254.0.0/16","::1/128","fc00::/7","fe80::/10",' \
  '}' \
  'var privateNets []*net.IPNet' \
  'var allowedPorts map[string]bool' \
  '' \
  'func init() {' \
  '	for _, cidr := range privateCIDRs {' \
  '		_, n, _ := net.ParseCIDR(cidr)' \
  '		privateNets = append(privateNets, n)' \
  '	}' \
  '	allowedPorts = make(map[string]bool)' \
  '	if ap := os.Getenv("ALLOWED_PORTS"); ap != "" {' \
  '		for _, p := range strings.Split(ap, ",") {' \
  '			if p = strings.TrimSpace(p); p != "" { allowedPorts[p] = true }' \
  '		}' \
  '	}' \
  '}' \
  '' \
  'func isPrivate(ip net.IP) bool {' \
  '	for _, n := range privateNets { if n.Contains(ip) { return true } }' \
  '	return false' \
  '}' \
  '' \
  'func checkAddr(hostport string) (string, error) {' \
  '	host, port, err := net.SplitHostPort(hostport)' \
  '	if err != nil { return "", err }' \
  '	ips, err := net.LookupIP(host)' \
  '	if err != nil { return "", err }' \
  '	for _, ip := range ips {' \
  '		if isPrivate(ip) && !allowedPorts[port] {' \
  '			return "", &net.AddrError{Err: "SSRF blocked", Addr: ip.String()}' \
  '		}' \
  '	}' \
  '	if len(ips) == 0 { return "", &net.AddrError{Err: "no IPs", Addr: host} }' \
  '	return net.JoinHostPort(ips[0].String(), port), nil' \
  '}' \
  '' \
  'func transfer(dst, src net.Conn) { defer dst.Close(); defer src.Close(); io.Copy(dst, src) }' \
  '' \
  'var hopHdrs = []string{"Connection","Keep-Alive","Proxy-Authenticate","Proxy-Authorization","Te","Trailers","Transfer-Encoding","Upgrade"}' \
  'func rmHop(h http.Header) { for _, k := range hopHdrs { h.Del(k) } }' \
  '' \
  'func main() {' \
  '	if hbFile := os.Getenv("HEARTBEAT_FILE"); hbFile != "" {' \
  '		idleTimeout := 900' \
  '		if t := os.Getenv("IDLE_TIMEOUT"); t != "" {' \
  '			if v, err := strconv.Atoi(t); err == nil && v > 0 { idleTimeout = v }' \
  '		}' \
  '		go func() {' \
  '			for {' \
  '				time.Sleep(30 * time.Second)' \
  '				info, err := os.Stat(hbFile)' \
  '				if err != nil || time.Since(info.ModTime()) > time.Duration(idleTimeout)*time.Second {' \
  '					log.Println("heartbeat stale, shutting down"); os.Exit(0)' \
  '				}' \
  '			}' \
  '		}()' \
  '	}' \
  '	port := os.Getenv("PORT")' \
  '	if port == "" { port = "3128" }' \
  '	log.Printf("proxy :%s", port)' \
  '	log.Fatal(http.ListenAndServe(":"+port, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {' \
  '		if r.Method == http.MethodConnect {' \
  '			addr, err := checkAddr(r.Host)' \
  '			if err != nil { http.Error(w, err.Error(), 403); return }' \
  '			up, err := net.DialTimeout("tcp", addr, 10*time.Second)' \
  '			if err != nil { http.Error(w, err.Error(), 502); return }' \
  '			w.WriteHeader(200)' \
  '			hj, ok := w.(http.Hijacker)' \
  '			if !ok { up.Close(); return }' \
  '			cl, _, err := hj.Hijack()' \
  '			if err != nil { up.Close(); return }' \
  '			go transfer(up, cl); go transfer(cl, up)' \
  '		} else {' \
  '			a := r.Host; if !strings.Contains(a, ":") { a += ":80" }' \
  '			if _, err := checkAddr(a); err != nil { http.Error(w, err.Error(), 403); return }' \
  '			r.RequestURI = ""; rmHop(r.Header)' \
  '			resp, err := http.DefaultTransport.RoundTrip(r)' \
  '			if err != nil { http.Error(w, err.Error(), 502); return }' \
  '			defer resp.Body.Close()' \
  '			rmHop(resp.Header)' \
  '			for k, vv := range resp.Header { for _, v := range vv { w.Header().Add(k, v) } }' \
  '			w.WriteHeader(resp.StatusCode); io.Copy(w, resp.Body)' \
  '		}' \
  '	})))' \
  '}' > main.go

RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /proxy .

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /proxy /proxy
ENTRYPOINT ["/proxy"]
