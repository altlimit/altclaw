### [ mail ] - Email via SMTP + IMAP

[ Send ]
* mail.send(opts: {host, user?, pass?, from, to, cc?, bcc?, subject, body, html?, attachments?}) → void
  Send email via SMTP with STARTTLS. Host defaults to port 587 if not specified.
  Secret templates ({{secrets.NAME}}) are expanded in: host, user, pass, from.
  - to/cc/bcc: arrays of email address strings
  - attachments: [{name: "file.pdf", path: "reports/file.pdf"}] — paths are workspace-jailed
  - html: optional HTML body (creates multipart message alongside plain text body)

  Example:
  ```javascript
  secret.set("SMTP_USER", "me@gmail.com");
  secret.set("SMTP_PASS", "abcd1234efgh5678");
  mail.send({
    host: "smtp.gmail.com",
    user: "{{secrets.SMTP_USER}}",
    pass: "{{secrets.SMTP_PASS}}",
    from: "{{secrets.SMTP_USER}}",
    to: ["recipient@example.com"],
    subject: "Hello",
    body: "Test email"
  });
  ```

[ Connect (IMAP) ]
* mail.connect(opts: {host, user, pass, tls?}) → client
  Connect to IMAP server. tls defaults to true (port 993). Set tls: false for port 143.
  Secret templates ({{secrets.NAME}}) are expanded in: host, user, pass.

[ Client Methods ]
* client.mailboxes() → string[]
  List all mailboxes on the server (e.g. "INBOX", "Sent", "Drafts", "[Gmail]/All Mail").
* client.list(mailbox?: string, opts?: {limit?, unseen?}) → {messages: [{uid, from, to, subject, date, flags}], total: number, mailbox: string}
  List messages in a mailbox (default "INBOX"). limit defaults to 20, newest first.
  Set unseen: true to only return unread messages. Returns total count and mailbox name for context.
* client.read(uid: number) → {from, to, cc, subject, date, body, html, attachments: [{name, size}]}
  Read a full message by UID. Returns parsed body, HTML, and attachment metadata.
* client.download(uid: number, filename: string, destPath: string) → void
  Save an attachment from a message to the workspace. destPath is workspace-jailed.
* client.flag(uid: number, flag: string) → void
  Add a flag to a message (e.g. "\\Seen", "\\Flagged").
* client.move(uid: number, mailbox: string) → void
  Move a message to another mailbox. Uses MOVE command with COPY+DELETE fallback.
* client.close() → void
  Disconnect from the IMAP server.
