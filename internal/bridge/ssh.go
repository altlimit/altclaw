package bridge

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dop251/goja"
	"golang.org/x/crypto/ssh"
)

// RegisterSSH adds the ssh namespace to the runtime.
//
//	ssh.connect(opts) → connection handle object
//	  opts: {host, user, key?, password?}
//
//	handle.exec(cmd)           → {stdout, stderr, exitCode}
//	handle.upload(local, remote) → void (local path workspace-jailed)
//	handle.download(remote, local) → void (local path workspace-jailed)
//	handle.close()             → void
func RegisterSSH(vm *goja.Runtime, workspace string) {
	sshObj := vm.NewObject()

	safe := func(op, path string) string {
		full, err := SanitizePath(workspace, path)
		if err != nil {
			Throwf(vm, "%s: %s", op, err)
		}
		return full
	}

	// ssh.connect(opts) → connection handle
	sshObj.Set("connect", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "ssh.connect requires an options object {host, user, key?, password?}")
		}
		opts := call.Arguments[0].ToObject(vm)
		CheckOpts(vm, "ssh.connect", opts, "host", "user", "key", "password")

		host := opts.Get("host").String()
		user := opts.Get("user").String()
		if host == "" || user == "" {
			Throw(vm, "ssh.connect: host and user are required")
		}

		// Add default port if missing
		if !strings.Contains(host, ":") {
			host += ":22"
		}

		config := &ssh.ClientConfig{
			User:            user,
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         30 * time.Second,
		}

		// Authentication: key takes priority over password
		keyVal := opts.Get("key")
		passVal := opts.Get("password")

		if keyVal != nil && !goja.IsUndefined(keyVal) && !goja.IsNull(keyVal) {
			keyStr := keyVal.String()
			signer, err := ssh.ParsePrivateKey([]byte(keyStr))
			if err != nil {
				logErr(vm, "ssh.connect", fmt.Errorf("parse private key: %w", err))
			}
			config.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
		} else if passVal != nil && !goja.IsUndefined(passVal) && !goja.IsNull(passVal) {
			config.Auth = []ssh.AuthMethod{ssh.Password(passVal.String())}
		} else {
			Throw(vm, "ssh.connect: either key or password is required")
		}

		client, err := ssh.Dial("tcp", host, config)
		if err != nil {
			logErr(vm, "ssh.connect", err)
		}

		// Build handle object
		handle := vm.NewObject()

		// handle.exec(cmd) → {stdout, stderr, exitCode}
		handle.Set("exec", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 1 {
				Throw(vm, "ssh.exec requires a command string")
			}
			cmd := call.Arguments[0].String()

			session, err := client.NewSession()
			if err != nil {
				logErr(vm, "ssh.exec", err)
			}
			defer session.Close()

			var stdoutBuf, stderrBuf strings.Builder
			session.Stdout = &stdoutBuf
			session.Stderr = &stderrBuf

			exitCode := 0
			if err := session.Run(cmd); err != nil {
				if exitErr, ok := err.(*ssh.ExitError); ok {
					exitCode = exitErr.ExitStatus()
				} else {
					logErr(vm, "ssh.exec", err)
				}
			}

			result := vm.NewObject()
			result.Set("stdout", stdoutBuf.String())
			result.Set("stderr", stderrBuf.String())
			result.Set("exitCode", exitCode)
			return result
		})

		// handle.upload(localPath, remotePath) — SCP-like upload via SFTP session
		handle.Set("upload", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 2 {
				Throw(vm, "ssh.upload requires local and remote paths")
			}
			localPath := safe("ssh.upload", call.Arguments[0].String())
			remotePath := call.Arguments[1].String()

			data, err := os.ReadFile(localPath)
			if err != nil {
				logErr(vm, "ssh.upload", err)
			}

			session, err := client.NewSession()
			if err != nil {
				logErr(vm, "ssh.upload", err)
			}
			defer session.Close()

			// Use cat to write file content via stdin
			session.Stdin = strings.NewReader(string(data))
			cmd := fmt.Sprintf("cat > %s", shellQuote(remotePath))
			if err := session.Run(cmd); err != nil {
				logErr(vm, "ssh.upload", err)
			}

			return goja.Undefined()
		})

		// handle.download(remotePath, localPath) — download file from remote
		handle.Set("download", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 2 {
				Throw(vm, "ssh.download requires remote and local paths")
			}
			remotePath := call.Arguments[0].String()
			localPath := safe("ssh.download", call.Arguments[1].String())

			session, err := client.NewSession()
			if err != nil {
				logErr(vm, "ssh.download", err)
			}
			defer session.Close()

			stdout, err := session.StdoutPipe()
			if err != nil {
				logErr(vm, "ssh.download", err)
			}

			cmd := fmt.Sprintf("cat %s", shellQuote(remotePath))
			if err := session.Start(cmd); err != nil {
				logErr(vm, "ssh.download", err)
			}

			f, err := os.Create(localPath)
			if err != nil {
				logErr(vm, "ssh.download", err)
			}
			_, err = io.Copy(f, stdout)
			f.Close()
			if err != nil {
				logErr(vm, "ssh.download", err)
			}
			session.Wait()

			return goja.Undefined()
		})

		// handle.close()
		handle.Set("close", func(call goja.FunctionCall) goja.Value {
			client.Close()
			return goja.Undefined()
		})

		return MethodProxy(vm, NameSSH, "ssh client", handle)
	})

	vm.Set(NameSSH, sshObj)
}

// shellQuote wraps a string in single quotes for safe shell usage.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
