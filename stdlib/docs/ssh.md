### [ ssh ] - Remote Command Execution

[ Connect ]
* ssh.connect(opts: {host, user, key?, password?}) → handle
  Connect to SSH server. Host defaults to port 22 if not specified.
  Use secret templates for credentials: key: "{{secrets.SSH_KEY}}"

[ Handle Methods ]
* handle.exec(cmd: string) → {stdout, stderr, exitCode}
  Execute a command on the remote server.
* handle.upload(localPath: string, remotePath: string) → void
  Upload a workspace file to the remote server. Local path is workspace-jailed.
* handle.download(remotePath: string, localPath: string) → void
  Download a file from the remote server. Local path is workspace-jailed.
* handle.close() → void
  Close the SSH connection.
