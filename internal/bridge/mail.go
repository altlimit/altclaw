package bridge

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"

	"altclaw.ai/internal/config"
	"github.com/dop251/goja"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
	gomail "github.com/emersion/go-message/mail"
)

// RegisterMail adds the mail namespace to the runtime.
//
//	mail.send(opts) — send an email via SMTP
//	mail.connect(opts) → connection handle with list, read, download, flag, move, close
func RegisterMail(vm *goja.Runtime, store *config.Store, ctxFn ...func() context.Context) {
	mailObj := vm.NewObject()
	getCtx := defaultCtxFn(ctxFn)

	// Derive workspace path from store for file-path jailing.
	workspace := ""
	if store != nil {
		if ws := store.Workspace(); ws != nil {
			workspace = ws.Path
		}
	}

	// expand resolves {{secrets.X}} templates in credential strings.
	expand := func(s string) string {
		return ExpandSecrets(getCtx(), store, s)
	}

	safe := func(op, path string) string {
		full, err := SanitizePath(workspace, path)
		if err != nil {
			Throwf(vm, "%s: %s", op, err)
		}
		return full
	}

	// ── mail.send(opts) ─────────────────────────────────────────────
	mailObj.Set("send", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "mail.send requires an options object {host, user, pass, from, to, subject, body}")
		}
		opts := call.Arguments[0].ToObject(vm)
		CheckOpts(vm, "mail.send", opts, "host", "user", "pass", "from", "to", "subject", "body", "html", "cc", "bcc", "attachments", "secure", "port")

		host := expand(optStr(vm, opts, "host"))
		user := expand(optStr(vm, opts, "user"))
		pass := expand(optStr(vm, opts, "pass"))
		from := expand(optStr(vm, opts, "from"))
		subject := optStr(vm, opts, "subject")
		body := optStr(vm, opts, "body")
		html := optStr(vm, opts, "html")

		if host == "" || from == "" {
			Throw(vm, "mail.send: host and from are required")
		}

		toVal := opts.Get("to")
		if toVal == nil || goja.IsUndefined(toVal) || goja.IsNull(toVal) {
			Throw(vm, "mail.send: to is required (array of addresses)")
		}
		to := exportStringSlice(vm, toVal)
		if len(to) == 0 {
			Throw(vm, "mail.send: to must contain at least one address")
		}
		cc := exportStringSliceOpt(vm, opts.Get("cc"))
		bcc := exportStringSliceOpt(vm, opts.Get("bcc"))

		// Add default port if missing
		if !strings.Contains(host, ":") {
			host += ":587"
		}

		// Build MIME message using go-message/mail
		var buf bytes.Buffer
		var hdr gomail.Header
		hdr.SetDate(time.Now())
		hdr.SetSubject(subject)
		hdr.SetAddressList("From", []*gomail.Address{{Address: from}})
		hdr.SetAddressList("To", addrList(to))
		if len(cc) > 0 {
			hdr.SetAddressList("Cc", addrList(cc))
		}

		// Collect attachments
		type attachment struct {
			name string
			path string
		}
		var attachments []attachment
		attVal := opts.Get("attachments")
		if attVal != nil && !goja.IsUndefined(attVal) && !goja.IsNull(attVal) {
			attObj := attVal.ToObject(vm)
			length := int(attObj.Get("length").ToInteger())
			for i := 0; i < length; i++ {
				item := attObj.Get(fmt.Sprintf("%d", i)).ToObject(vm)
				name := optStr(vm, item, "name")
				path := optStr(vm, item, "path")
				if name == "" || path == "" {
					Throwf(vm, "mail.send: attachment[%d] requires name and path", i)
				}
				attachments = append(attachments, attachment{name: name, path: safe("mail.send", path)})
			}
		}

		hasAttachments := len(attachments) > 0
		hasHTML := html != ""

		if hasAttachments || hasHTML {
			// Multipart message
			w, err := gomail.CreateWriter(&buf, hdr)
			if err != nil {
				logErr(vm, "mail.send", err)
			}

			// Text part
			if body != "" {
				var ih gomail.InlineHeader
				ih.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
				pw, err := w.CreateInline()
				if err != nil {
					logErr(vm, "mail.send", err)
				}
				part, err := pw.CreatePart(ih)
				if err != nil {
					logErr(vm, "mail.send", err)
				}
				part.Write([]byte(body))
				part.Close()
				pw.Close()
			}

			// HTML part
			if hasHTML {
				var ih gomail.InlineHeader
				ih.SetContentType("text/html", map[string]string{"charset": "utf-8"})
				pw, err := w.CreateInline()
				if err != nil {
					logErr(vm, "mail.send", err)
				}
				part, err := pw.CreatePart(ih)
				if err != nil {
					logErr(vm, "mail.send", err)
				}
				part.Write([]byte(html))
				part.Close()
				pw.Close()
			}

			// Attachments
			for _, att := range attachments {
				data, err := os.ReadFile(att.path)
				if err != nil {
					logErr(vm, "mail.send", err)
				}
				var ah gomail.AttachmentHeader
				ah.SetFilename(att.name)
				ct := mime.TypeByExtension(filepath.Ext(att.name))
				if ct == "" {
					ct = "application/octet-stream"
				}
				ah.SetContentType(ct, nil)
				aw, err := w.CreateAttachment(ah)
				if err != nil {
					logErr(vm, "mail.send", err)
				}
				aw.Write(data)
				aw.Close()
			}

			w.Close()
		} else {
			// Simple plain text
			pw, err := gomail.CreateSingleInlineWriter(&buf, hdr)
			if err != nil {
				logErr(vm, "mail.send", err)
			}
			pw.Write([]byte(body))
			pw.Close()
		}

		// All recipients
		allRecipients := make([]string, 0, len(to)+len(cc)+len(bcc))
		allRecipients = append(allRecipients, to...)
		allRecipients = append(allRecipients, cc...)
		allRecipients = append(allRecipients, bcc...)

		// SMTP send with STARTTLS
		smtpHost, _, _ := net.SplitHostPort(host)
		if smtpHost == "" {
			smtpHost = host
		}

		c, err := smtp.Dial(host)
		if err != nil {
			logErr(vm, "mail.send", err)
		}
		defer c.Close()

		// STARTTLS
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: smtpHost}); err != nil {
				logErr(vm, "mail.send", err)
			}
		}

		// Auth
		if user != "" && pass != "" {
			auth := smtp.PlainAuth("", user, pass, smtpHost)
			if err := c.Auth(auth); err != nil {
				logErr(vm, "mail.send", err)
			}
		}

		if err := c.Mail(from); err != nil {
			logErr(vm, "mail.send", err)
		}
		for _, rcpt := range allRecipients {
			if err := c.Rcpt(rcpt); err != nil {
				logErr(vm, "mail.send", err)
			}
		}

		wc, err := c.Data()
		if err != nil {
			logErr(vm, "mail.send", err)
		}
		if _, err := wc.Write(buf.Bytes()); err != nil {
			logErr(vm, "mail.send", err)
		}
		if err := wc.Close(); err != nil {
			logErr(vm, "mail.send", err)
		}
		c.Quit()

		return goja.Undefined()
	})

	// ── mail.connect(opts) → handle ─────────────────────────────────
	mailObj.Set("connect", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "mail.connect requires an options object {host, user, pass, tls?}")
		}
		opts := call.Arguments[0].ToObject(vm)
		CheckOpts(vm, "mail.connect", opts, "host", "user", "pass", "tls")

		host := expand(optStr(vm, opts, "host"))
		user := expand(optStr(vm, opts, "user"))
		pass := expand(optStr(vm, opts, "pass"))
		if host == "" || user == "" || pass == "" {
			Throw(vm, "mail.connect: host, user, and pass are required")
		}

		// Default to TLS
		useTLS := true
		tlsVal := opts.Get("tls")
		if tlsVal != nil && !goja.IsUndefined(tlsVal) && !goja.IsNull(tlsVal) {
			useTLS = tlsVal.ToBoolean()
		}

		// Add default port if missing
		if !strings.Contains(host, ":") {
			if useTLS {
				host += ":993"
			} else {
				host += ":143"
			}
		}

		imapOpts := &imapclient.Options{
			WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader},
		}

		var client *imapclient.Client
		var err error
		if useTLS {
			client, err = imapclient.DialTLS(host, imapOpts)
		} else {
			client, err = imapclient.DialInsecure(host, imapOpts)
		}
		if err != nil {
			logErr(vm, "mail.connect", err)
		}

		// Login
		if loginErr := client.Login(user, pass).Wait(); loginErr != nil {
			client.Close()
			logErr(vm, "mail.connect", loginErr)
		}

		// Build handle object
		handle := vm.NewObject()

		// handle.list(mailbox, opts?) → [{uid, from, to, subject, date, flags}]
		handle.Set("list", func(call goja.FunctionCall) goja.Value {
			mailbox := "INBOX"
			if len(call.Arguments) >= 1 {
				mailbox = call.Arguments[0].String()
			}

			limit := 20
			unseenOnly := false
			if len(call.Arguments) >= 2 {
				listOpts := call.Arguments[1].ToObject(vm)
				if v := listOpts.Get("limit"); v != nil && !goja.IsUndefined(v) {
					limit = int(v.ToInteger())
				}
				if v := listOpts.Get("unseen"); v != nil && !goja.IsUndefined(v) {
					unseenOnly = v.ToBoolean()
				}
			}

			// Select mailbox
			if _, err := client.Select(mailbox, nil).Wait(); err != nil {
				logErr(vm, "mail.list", err)
			}

			// Search for messages
			criteria := &imap.SearchCriteria{}
			if unseenOnly {
				criteria.NotFlag = []imap.Flag{imap.FlagSeen}
			}

			searchCmd := client.UIDSearch(criteria, &imap.SearchOptions{ReturnAll: true})
			searchData, err := searchCmd.Wait()
			if err != nil {
				logErr(vm, "mail.list", err)
			}

			// Get UIDs from search result
			allUIDs := searchData.AllUIDs()
			if len(allUIDs) == 0 {
				return vm.ToValue(map[string]interface{}{
					"messages": []interface{}{},
					"total":    0,
					"mailbox":  mailbox,
				})
			}

			// Take only the last `limit` UIDs (newest)
			start := 0
			if len(allUIDs) > limit {
				start = len(allUIDs) - limit
			}
			selectedUIDs := allUIDs[start:]

			uidSet := imap.UIDSetNum(selectedUIDs...)

			// Fetch envelope + flags
			fetchCmd := client.Fetch(uidSet, &imap.FetchOptions{
				Envelope: true,
				Flags:    true,
				UID:      true,
			})
			defer fetchCmd.Close()

			var results []interface{}
			for {
				msg := fetchCmd.Next()
				if msg == nil {
					break
				}
				buf, err := msg.Collect()
				if err != nil {
					continue
				}
				entry := map[string]interface{}{
					"uid":     uint32(buf.UID),
					"subject": "",
					"from":    "",
					"to":      "",
					"date":    "",
					"flags":   []interface{}{},
				}
				if buf.Envelope != nil {
					entry["subject"] = buf.Envelope.Subject
					entry["date"] = buf.Envelope.Date.Format(time.RFC3339)
					if len(buf.Envelope.From) > 0 {
						entry["from"] = buf.Envelope.From[0].Addr()
					}
					if len(buf.Envelope.To) > 0 {
						addrs := make([]string, len(buf.Envelope.To))
						for i, a := range buf.Envelope.To {
							addrs[i] = a.Addr()
						}
						entry["to"] = strings.Join(addrs, ", ")
					}
				}
				flags := make([]interface{}, len(buf.Flags))
				for i, f := range buf.Flags {
					flags[i] = string(f)
				}
				entry["flags"] = flags
				results = append(results, entry)
			}

			return vm.ToValue(map[string]interface{}{
				"messages": results,
				"total":    len(results),
				"mailbox":  mailbox,
			})
		})

		// handle.read(uid) → {from, to, cc, subject, date, body, html, attachments}
		handle.Set("read", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 1 {
				Throw(vm, "mail.read requires a UID argument")
			}
			uid := imap.UID(call.Arguments[0].ToInteger())

			uidSet := imap.UIDSetNum(uid)
			bodySection := &imap.FetchItemBodySection{Peek: true}
			fetchCmd := client.Fetch(uidSet, &imap.FetchOptions{
				Envelope:    true,
				UID:         true,
				BodySection: []*imap.FetchItemBodySection{bodySection},
			})
			defer fetchCmd.Close()

			msg := fetchCmd.Next()
			if msg == nil {
				Throw(vm, "mail.read: message not found")
			}

			buf, err := msg.Collect()
			if err != nil {
				logErr(vm, "mail.read", err)
			}

			result := map[string]interface{}{
				"from":        "",
				"to":          "",
				"cc":          "",
				"subject":     "",
				"date":        "",
				"body":        "",
				"html":        "",
				"attachments": []interface{}{},
			}

			if buf.Envelope != nil {
				result["subject"] = buf.Envelope.Subject
				result["date"] = buf.Envelope.Date.Format(time.RFC3339)
				if len(buf.Envelope.From) > 0 {
					result["from"] = buf.Envelope.From[0].Addr()
				}
				if len(buf.Envelope.To) > 0 {
					addrs := make([]string, len(buf.Envelope.To))
					for i, a := range buf.Envelope.To {
						addrs[i] = a.Addr()
					}
					result["to"] = strings.Join(addrs, ", ")
				}
				if len(buf.Envelope.Cc) > 0 {
					addrs := make([]string, len(buf.Envelope.Cc))
					for i, a := range buf.Envelope.Cc {
						addrs[i] = a.Addr()
					}
					result["cc"] = strings.Join(addrs, ", ")
				}
			}

			// Parse body
			rawBody := buf.FindBodySection(bodySection)
			if rawBody != nil {
				mr, err := gomail.CreateReader(bytes.NewReader(rawBody))
				if err == nil {
					var attachments []interface{}
					for {
						part, err := mr.NextPart()
						if err != nil {
							break
						}
						switch h := part.Header.(type) {
						case *gomail.InlineHeader:
							ct, _, _ := h.ContentType()
							data, _ := io.ReadAll(part.Body)
							if strings.HasPrefix(ct, "text/plain") {
								result["body"] = string(data)
							} else if strings.HasPrefix(ct, "text/html") {
								result["html"] = string(data)
							}
						case *gomail.AttachmentHeader:
							filename, _ := h.Filename()
							data, _ := io.ReadAll(part.Body)
							attachments = append(attachments, map[string]interface{}{
								"name": filename,
								"size": len(data),
							})
						}
					}
					result["attachments"] = attachments
				}
			}

			return vm.ToValue(result)
		})

		// handle.download(uid, filename, destPath) — save attachment
		handle.Set("download", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 3 {
				Throw(vm, "mail.download requires uid, filename, and destPath")
			}
			uid := imap.UID(call.Arguments[0].ToInteger())
			targetFilename := call.Arguments[1].String()
			destPath := safe("mail.download", call.Arguments[2].String())

			uidSet := imap.UIDSetNum(uid)
			bodySection := &imap.FetchItemBodySection{Peek: true}
			fetchCmd := client.Fetch(uidSet, &imap.FetchOptions{
				UID:         true,
				BodySection: []*imap.FetchItemBodySection{bodySection},
			})
			defer fetchCmd.Close()

			msg := fetchCmd.Next()
			if msg == nil {
				Throw(vm, "mail.download: message not found")
			}

			msgBuf, err := msg.Collect()
			if err != nil {
				logErr(vm, "mail.download", err)
			}

			rawBody := msgBuf.FindBodySection(bodySection)
			if rawBody == nil {
				Throw(vm, "mail.download: no body section")
			}

			mr, err := gomail.CreateReader(bytes.NewReader(rawBody))
			if err != nil {
				logErr(vm, "mail.download", err)
			}

			found := false
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				if ah, ok := part.Header.(*gomail.AttachmentHeader); ok {
					filename, _ := ah.Filename()
					if strings.EqualFold(filename, targetFilename) {
						// Ensure parent dir exists
						if dir := filepath.Dir(destPath); dir != "" {
							os.MkdirAll(dir, 0755)
						}
						data, err := io.ReadAll(part.Body)
						if err != nil {
							logErr(vm, "mail.download", err)
						}
						if err := os.WriteFile(destPath, data, 0644); err != nil {
							logErr(vm, "mail.download", err)
						}
						found = true
						break
					}
				}
			}

			if !found {
				Throwf(vm, "mail.download: attachment %q not found", targetFilename)
			}

			return goja.Undefined()
		})

		// handle.flag(uid, flag) — add a flag
		handle.Set("flag", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 2 {
				Throw(vm, "mail.flag requires uid and flag arguments")
			}
			uid := imap.UID(call.Arguments[0].ToInteger())
			flag := imap.Flag(call.Arguments[1].String())

			uidSet := imap.UIDSetNum(uid)
			storeCmd := client.Store(uidSet, &imap.StoreFlags{
				Op:    imap.StoreFlagsAdd,
				Flags: []imap.Flag{flag},
			}, nil)
			if err := storeCmd.Close(); err != nil {
				logErr(vm, "mail.flag", err)
			}

			return goja.Undefined()
		})

		// handle.move(uid, mailbox) — move message
		handle.Set("move", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 2 {
				Throw(vm, "mail.move requires uid and mailbox arguments")
			}
			uid := imap.UID(call.Arguments[0].ToInteger())
			destMailbox := call.Arguments[1].String()

			uidSet := imap.UIDSetNum(uid)

			// Try MOVE first (IMAP extension), fallback to COPY+DELETE
			moveCmd := client.Move(uidSet, destMailbox)
			if _, err := moveCmd.Wait(); err != nil {
				// Fallback: COPY + mark deleted + expunge
				copyCmd := client.Copy(uidSet, destMailbox)
				if _, err := copyCmd.Wait(); err != nil {
					logErr(vm, "mail.move", err)
				}
				delCmd := client.Store(uidSet, &imap.StoreFlags{
					Op:    imap.StoreFlagsAdd,
					Flags: []imap.Flag{imap.FlagDeleted},
				}, nil)
				delCmd.Close()
				client.Expunge()
			}

			return goja.Undefined()
		})

		// handle.mailboxes() → ["INBOX", "Sent", ...]
		handle.Set("mailboxes", func(call goja.FunctionCall) goja.Value {
			listCmd := client.List("", "*", nil)
			data, err := listCmd.Collect()
			if err != nil {
				logErr(vm, "mail.mailboxes", err)
			}
			names := make([]interface{}, len(data))
			for i, d := range data {
				names[i] = d.Mailbox
			}
			return vm.ToValue(names)
		})

		// handle.close() — disconnect
		handle.Set("close", func(call goja.FunctionCall) goja.Value {
			client.Logout().Wait()
			client.Close()
			return goja.Undefined()
		})

		return MethodProxy(vm, NameMail, "mail client", handle)
	})

	vm.Set(NameMail, mailObj)
}

// ── helpers ─────────────────────────────────────────────────────────

func optStr(vm *goja.Runtime, obj *goja.Object, key string) string {
	v := obj.Get(key)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

func addrList(addrs []string) []*gomail.Address {
	result := make([]*gomail.Address, len(addrs))
	for i, a := range addrs {
		result[i] = &gomail.Address{Address: strings.TrimSpace(a)}
	}
	return result
}

func exportStringSlice(vm *goja.Runtime, val goja.Value) []string {
	obj := val.ToObject(vm)
	length := int(obj.Get("length").ToInteger())
	result := make([]string, 0, length)
	for i := 0; i < length; i++ {
		result = append(result, obj.Get(fmt.Sprintf("%d", i)).String())
	}
	return result
}

func exportStringSliceOpt(vm *goja.Runtime, val goja.Value) []string {
	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
		return nil
	}
	return exportStringSlice(vm, val)
}
