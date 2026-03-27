package bridge

import (
	"net"

	"github.com/dop251/goja"
)

// RegisterDNS adds the dns namespace to the runtime.
//
//	dns.lookup(hostname, type?)  → results
//	dns.reverse(ip)             → hostnames
func RegisterDNS(vm *goja.Runtime) {
	dnsObj := vm.NewObject()

	// dns.lookup(hostname, type?) — resolve DNS records
	dnsObj.Set("lookup", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "dns.lookup requires a hostname")
		}
		hostname := call.Arguments[0].String()
		recordType := "A"
		if len(call.Arguments) >= 2 {
			recordType = call.Arguments[1].String()
		}

		switch recordType {
		case "A", "AAAA":
			ips, err := net.LookupHost(hostname)
			if err != nil {
				logErr(vm, "dns.lookup", err)
			}
			// Filter by type
			var results []string
			for _, ip := range ips {
				parsed := net.ParseIP(ip)
				if parsed == nil {
					continue
				}
				if recordType == "A" && parsed.To4() != nil {
					results = append(results, ip)
				} else if recordType == "AAAA" && parsed.To4() == nil {
					results = append(results, ip)
				}
			}
			return vm.ToValue(results)

		case "MX":
			mxs, err := net.LookupMX(hostname)
			if err != nil {
				logErr(vm, "dns.lookup", err)
			}
			var results []map[string]interface{}
			for _, mx := range mxs {
				results = append(results, map[string]interface{}{
					"host":     mx.Host,
					"priority": mx.Pref,
				})
			}
			return vm.ToValue(results)

		case "TXT":
			txts, err := net.LookupTXT(hostname)
			if err != nil {
				logErr(vm, "dns.lookup", err)
			}
			return vm.ToValue(txts)

		case "CNAME":
			cname, err := net.LookupCNAME(hostname)
			if err != nil {
				logErr(vm, "dns.lookup", err)
			}
			return vm.ToValue([]string{cname})

		case "NS":
			nss, err := net.LookupNS(hostname)
			if err != nil {
				logErr(vm, "dns.lookup", err)
			}
			var results []string
			for _, ns := range nss {
				results = append(results, ns.Host)
			}
			return vm.ToValue(results)

		default:
			Throwf(vm, "dns.lookup: unsupported record type %q (use A, AAAA, MX, TXT, CNAME, NS)", recordType)
		}
		return goja.Undefined()
	})

	// dns.reverse(ip) — reverse DNS lookup
	dnsObj.Set("reverse", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "dns.reverse requires an IP address")
		}
		ip := call.Arguments[0].String()
		names, err := net.LookupAddr(ip)
		if err != nil {
			logErr(vm, "dns.reverse", err)
		}
		return vm.ToValue(names)
	})

	vm.Set(NameDNS, dnsObj)
}
