### [ dns ] - DNS Lookups

[ Lookup ]
* dns.lookup(hostname: string, type?: string) → string[] | object[]
  Resolve DNS records. Type: "A" (default), "AAAA", "MX", "TXT", "CNAME", "NS".
  MX returns [{host, priority}]. Others return string arrays.
* dns.reverse(ip: string) → string[]
  Reverse DNS lookup.
