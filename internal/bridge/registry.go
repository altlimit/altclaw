package bridge

// Bridge name constants — the JS global name each native bridge is registered under.
// Use these instead of raw string literals in vm.Set(), vm.Get(), and Builtins entries.
const (
	NameFS     = "fs"
	NameFetch  = "fetch"
	NameSys    = "sys"
	NameUI     = "ui"
	NameMem    = "mem"
	NameCron   = "cron"
	NameAgent  = "agent"
	NameDoc    = "doc"
	NameCrypto = "crypto"
	NameTask   = "task"
	NameDB     = "db"
	NameBlob   = "blob"
	NameGit    = "git"
	NameSecret = "secret"
	NameCSV    = "csv"
	NameMod    = "mod"
	NameLog    = "log"
	NameDNS    = "dns"
	NameCache  = "cache"
	NameZip    = "zip"
	NameImage  = "img"
	NameSSH    = "ssh"
	NameMail    = "mail"
	NameChat    = "chat"
	NameBrowser = "browser"
)

// BuiltinInfo describes a native bridge module available in the JS runtime.
// Each entry documents the global name, how it's exposed (via require() or as
// a direct global), and a one-line description used by doc.list().
//
// Add a new entry here whenever you add a new RegisterXxx() function so that:
//   - doc.list() returns it automatically
//   - engine.go's require() shim is kept in sync via BuiltinNames()
type BuiltinInfo struct {
	Name        string // JS name used in require("name") or as a global
	Description string // One-liner shown by doc.list()
	ViaRequire  bool   // true → require("name") returns the global; false → global only
}

// Builtins is the canonical list of all native bridge modules.
// ORDER MATTERS: it determines the order returned by doc.list().
var Builtins = []BuiltinInfo{
	{Name: NameFS, Description: "Workspace-jailed filesystem: read, write, list, grep, patch, append, stat", ViaRequire: true},
	{Name: NameFetch, Description: "HTTP client with automatic response size limits and streaming download support", ViaRequire: true},
	{Name: NameSys, Description: "Command execution: sys.call, sys.spawn, sys.getOutput, sys.terminate, sys.setImage", ViaRequire: true},
	{Name: NameUI, Description: "User interaction: ui.log, ui.ask, ui.file, ui.notify", ViaRequire: true},
	{Name: NameMem, Description: "Persistent key-value memory: mem.set/get (workspace), mem.setUser/getUser (global)", ViaRequire: true},
	{Name: NameCron, Description: "Background scheduled tasks: cron.add, cron.rm, cron.list", ViaRequire: true},
	{Name: NameAgent, Description: "Spawn sub-agents: agent.run(task, provider?), agent.result(id)", ViaRequire: true},
	{Name: NameDoc, Description: "Module documentation: doc.read, doc.list, doc.find, doc.all", ViaRequire: true},
	{Name: NameCrypto, Description: "Cryptographic utilities: hash, hmac, aes encrypt/decrypt, random bytes", ViaRequire: true},
	{Name: NameTask, Description: "Parallel JS tasks within the same VM: task.run, task.join", ViaRequire: true},
	{Name: NameDB, Description: "SQLite database per workspace: db.open, db.exec, db.query, db.close", ViaRequire: true},
	{Name: NameBlob, Description: "Large binary object storage with streaming read/write support", ViaRequire: true},
	{Name: NameGit, Description: "Git operations on the workspace repo: log, diff, restore, snapshot", ViaRequire: true},
	{Name: NameSecret, Description: "Encrypted secret store: secret.set, secret.get, secret.delete", ViaRequire: true},
	{Name: NameCSV, Description: "CSV reader/writer: csv.read (streaming or buffered), csv.write, csv.append", ViaRequire: true},
	{Name: NameMod, Description: "Module management: mod.search, mod.install, mod.remove, mod.info, mod.list", ViaRequire: true},
	{Name: NameLog, Description: "Application logs: log.recent, log.search, log.info, log.warn, log.error, log.debug", ViaRequire: true},
	{Name: NameDNS, Description: "DNS lookups: dns.lookup, dns.reverse", ViaRequire: true},
	{Name: NameCache, Description: "TTL key-value cache with rate limiting: cache.set, cache.get, cache.del, cache.rate", ViaRequire: true},
	{Name: NameZip, Description: "Archive operations: zip.create, zip.extract, zip.list (zip + tar.gz)", ViaRequire: true},
	{Name: NameImage, Description: "Image manipulation: img.info, img.resize, img.crop, img.convert, img.rotate", ViaRequire: true},
	{Name: NameSSH, Description: "Remote execution via SSH: ssh.connect → exec, upload, download", ViaRequire: true},
	{Name: NameMail, Description: "Email via SMTP + IMAP: mail.send, mail.connect → list, read, download, flag, move", ViaRequire: true},
	{Name: NameChat, Description: "Cross-conversation access: chat.list, chat.read", ViaRequire: true},
	{Name: NameBrowser, Description: "Headless browser automation: browser.open → page.click, type, eval, screenshot, pdf", ViaRequire: true},
}

// BuiltinNames returns the set of names that engine's require() shim intercepts
// (all builtins where ViaRequire is true).
func BuiltinNames() map[string]bool {
	m := make(map[string]bool, len(Builtins))
	for _, b := range Builtins {
		if b.ViaRequire {
			m[b.Name] = true
		}
	}
	return m
}
