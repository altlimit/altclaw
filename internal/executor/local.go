package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ansiRe strips ANSI escape sequences (colors, cursor movement, etc.) from pseudo-TTY output.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x1b]*\x1b\\|\x1b[^\[\]]`)

// Local implements Executor using os/exec for the host machine.
type Local struct {
	Workspace string   // root workspace directory for path context
	Whitelist []string // allowed commands (empty = allow all)

	mu      sync.Mutex
	spawned map[string]*spawnedProcess
	popened map[string]*popenProcess
}

type spawnedProcess struct {
	cmd    *exec.Cmd
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	done   chan struct{}
}

// popenProcess tracks an interactive process with stdin/stdout pipes.
type popenProcess struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan string
	err   error // set when scanner goroutine finishes
	done  chan struct{}
}

// NewLocal creates a local executor bound to the given workspace.
func NewLocal(workspace string, whitelist []string) *Local {
	return &Local{
		Workspace: workspace,
		Whitelist: whitelist,
		spawned:   make(map[string]*spawnedProcess),
		popened:   make(map[string]*popenProcess),
	}
}

func (l *Local) IsAllowed(cmd string) bool {
	if len(l.Whitelist) == 0 {
		return true // empty = allow all; bridge layer handles the confirm gate
	}
	for _, w := range l.Whitelist {
		if w == "*" {
			return true // wildcard = allow all
		}
		if w == cmd {
			return true
		}
	}
	return false
}

// Run executes a command synchronously.
func (l *Local) Run(ctx context.Context, cmd string, args []string) (*Result, error) {
	if !l.IsAllowed(cmd) {
		return nil, fmt.Errorf("command %q not in whitelist", cmd)
	}

	c := commandContext(ctx, cmd, args...)
	c.Dir = l.Workspace

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	envMap := EnvFrom(ctx)
	if len(envMap) > 0 {
		cmdEnv := os.Environ()
		for k, v := range envMap {
			cmdEnv = append(cmdEnv, fmt.Sprintf("%s=%s", k, v))
		}
		c.Env = cmdEnv
	}

	err := c.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() != nil {
			// Context cancelled (Ctrl+C) — propagate so cleanup runs
			return nil, ctx.Err()
		} else {
			// Command not found or other exec error — return as stderr
			return &Result{
				Stdout:   stdout.String(),
				Stderr:   err.Error(),
				ExitCode: -1,
			}, nil
		}
	}

	return &Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}

// Spawn starts a command asynchronously and returns a handle ID.
func (l *Local) Spawn(ctx context.Context, cmd string, args []string) (string, error) {
	if !l.IsAllowed(cmd) {
		return "", fmt.Errorf("command %q not in whitelist", cmd)
	}

	c := commandContext(ctx, cmd, args...)
	c.Dir = l.Workspace

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	envMap := EnvFrom(ctx)
	if len(envMap) > 0 {
		cmdEnv := os.Environ()
		for k, v := range envMap {
			cmdEnv = append(cmdEnv, fmt.Sprintf("%s=%s", k, v))
		}
		c.Env = cmdEnv
	}

	if err := c.Start(); err != nil {
		return "", fmt.Errorf("spawn %q: %w", cmd, err)
	}

	id := uuid.New().String()
	sp := &spawnedProcess{
		cmd:    c,
		stdout: &stdout,
		stderr: &stderr,
		done:   make(chan struct{}),
	}

	go func() {
		_ = c.Wait()
		close(sp.done)
	}()

	l.mu.Lock()
	l.spawned[id] = sp
	l.mu.Unlock()

	return id, nil
}

// GetOutput retrieves the current stdout buffer for a spawned process.
func (l *Local) GetOutput(handleID string) (string, error) {
	l.mu.Lock()
	sp, ok := l.spawned[handleID]
	l.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("unknown handle %q", handleID)
	}
	return sp.stdout.String(), nil
}

// Terminate kills a spawned or popen'd process by handle ID.
func (l *Local) Terminate(handleID string) error {
	l.mu.Lock()
	sp, ok := l.spawned[handleID]
	pp, okP := l.popened[handleID]
	l.mu.Unlock()
	if okP {
		if pp.stdin != nil {
			pp.stdin.Close()
		}
		if pp.cmd.Process != nil {
			return pp.cmd.Process.Kill()
		}
		return nil
	}
	if !ok {
		return fmt.Errorf("unknown handle %q", handleID)
	}
	if sp.cmd.Process != nil {
		return sp.cmd.Process.Kill()
	}
	return nil
}

// Cleanup kills all spawned and popen'd processes.
func (l *Local) Cleanup() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, sp := range l.spawned {
		if sp.cmd.Process != nil {
			_ = sp.cmd.Process.Kill()
		}
		delete(l.spawned, id)
	}
	for id, pp := range l.popened {
		if pp.stdin != nil {
			pp.stdin.Close()
		}
		if pp.cmd.Process != nil {
			_ = pp.cmd.Process.Kill()
		}
		delete(l.popened, id)
	}
	return nil
}

// SetImage is a no-op for local executor.
func (l *Local) SetImage(image string, opts ImageOpts, sessionID string) {}

// CleanupSession is a no-op for local executor.
func (l *Local) CleanupSession(sessionID string) {}

// Popen starts a command with stdin piped and stdout line-buffered.
func (l *Local) Popen(ctx context.Context, cmd string, args []string) (string, error) {
	if !l.IsAllowed(cmd) {
		return "", fmt.Errorf("command %q not in whitelist", cmd)
	}

	c := exec.CommandContext(ctx, cmd, args...)
	hideWindow(c)
	c.Dir = l.Workspace

	envMap := EnvFrom(ctx)
	if len(envMap) > 0 {
		cmdEnv := os.Environ()
		for k, v := range envMap {
			cmdEnv = append(cmdEnv, fmt.Sprintf("%s=%s", k, v))
		}
		c.Env = cmdEnv
	}

	stdinPipe, err := c.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("popen stdin pipe: %w", err)
	}

	stdoutPipe, err := c.StdoutPipe()
	if err != nil {
		stdinPipe.Close()
		return "", fmt.Errorf("popen stdout pipe: %w", err)
	}

	if err := c.Start(); err != nil {
		stdinPipe.Close()
		return "", fmt.Errorf("popen start: %w", err)
	}

	id := uuid.New().String()
	pp := &popenProcess{
		cmd:   c,
		stdin: stdinPipe,
		lines: make(chan string, 64),
		done:  make(chan struct{}),
	}

	// Goroutine reads stdout line by line and sends to channel
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer
		for scanner.Scan() {
			line := strings.TrimRight(scanner.Text(), "\r")
			line = ansiRe.ReplaceAllString(line, "") // strip ANSI escape codes
			pp.lines <- line
		}
		pp.err = scanner.Err()
		close(pp.done)
	}()

	l.mu.Lock()
	l.popened[id] = pp
	l.mu.Unlock()

	return id, nil
}

// WriteStdin writes data to the stdin of a Popen'd process.
func (l *Local) WriteStdin(handleID string, data string) error {
	l.mu.Lock()
	pp, ok := l.popened[handleID]
	l.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown popen handle %q", handleID)
	}
	_, err := io.WriteString(pp.stdin, data)
	return err
}

// ReadLine reads the next line from stdout of a Popen'd process.
func (l *Local) ReadLine(handleID string, timeoutMs int) (string, error) {
	l.mu.Lock()
	pp, ok := l.popened[handleID]
	l.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("unknown popen handle %q", handleID)
	}

	timeout := time.Duration(timeoutMs) * time.Millisecond
	select {
	case line, ok := <-pp.lines:
		if !ok {
			return "", fmt.Errorf("process exited")
		}
		return line, nil
	case <-pp.done:
		// Check if there are buffered lines
		select {
		case line := <-pp.lines:
			return line, nil
		default:
			if pp.err != nil {
				return "", fmt.Errorf("process read error: %w", pp.err)
			}
			return "", fmt.Errorf("process exited")
		}
	case <-time.After(timeout):
		return "", fmt.Errorf("readLine timeout after %dms", timeoutMs)
	}
}

// Info returns environment introspection data for the local host.
func (l *Local) Info(ctx context.Context) (map[string]any, error) {
	osInfo := map[string]any{
		"type": runtime.GOOS,
		"arch": runtime.GOARCH,
	}

	// Distro/version from /etc/os-release (Linux)
	if runtime.GOOS == "linux" {
		if f, err := os.Open("/etc/os-release"); err == nil {
			defer f.Close()
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := scanner.Text()
				if k, v, ok := strings.Cut(line, "="); ok {
					v = strings.Trim(v, `"`)
					switch k {
					case "ID":
						osInfo["distro"] = v
					case "VERSION_ID":
						osInfo["version"] = v
					}
				}
			}
		}
	} else if runtime.GOOS == "darwin" {
		if out, err := commandContext(ctx, "sw_vers", "-productVersion").Output(); err == nil {
			osInfo["distro"] = "macos"
			osInfo["version"] = strings.TrimSpace(string(out))
		}
	}

	// Kernel version
	if runtime.GOOS != "windows" {
		if out, err := commandContext(ctx, "uname", "-r").Output(); err == nil {
			osInfo["kernel"] = strings.TrimSpace(string(out))
		}
	}

	// Resources
	resources := map[string]any{
		"cpus": runtime.NumCPU(),
	}

	// Memory from /proc/meminfo (Linux)
	if runtime.GOOS == "linux" {
		if f, err := os.Open("/proc/meminfo"); err == nil {
			defer f.Close()
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				if strings.HasPrefix(scanner.Text(), "MemTotal:") {
					fields := strings.Fields(scanner.Text())
					if len(fields) >= 2 {
						if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
							resources["memory_total_mb"] = kb / 1024
						}
					}
					break
				}
			}
		}
	}

	// Disk free space (platform-specific helper)
	if freeMB, err := diskFreeMB(l.Workspace); err == nil {
		resources["disk_free_mb"] = freeMB
	}

	// Runtimes — probe for common tools
	runtimes := probeRuntimes(ctx)

	// Capabilities
	capabilities := map[string]any{
		"internet_access": true,
		"executor":        "local",
	}

	// Paths
	paths := map[string]any{
		"workspace": l.Workspace,
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths["home"] = home
	}

	return map[string]any{
		"os":           osInfo,
		"resources":    resources,
		"runtimes":     runtimes,
		"capabilities": capabilities,
		"paths":        paths,
	}, nil
}

// versionRe matches common version strings: "1.2.3", "24.0.7", "3.11", etc.
var versionRe = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?(?:[._\-]\w+)*`)

// probeRuntimes checks for common tools and returns their versions.
// Uses a universal regex to extract version numbers from command output.
func probeRuntimes(ctx context.Context) map[string]any {
	runtimes := map[string]any{}

	// Each entry: {label, binary, version-flag}
	probes := [][3]string{
		{"node", "node", "--version"},
		{"bun", "bun", "--version"},
		{"python", "python3", "--version"},
		{"git", "git", "--version"},
		{"go", "go", "version"},
		{"ffmpeg", "ffmpeg", "-version"},
		{"ruby", "ruby", "--version"},
		{"java", "java", "-version"},
		{"php", "php", "--version"},
		{"rust", "rustc", "--version"},
		{"cargo", "cargo", "--version"},
		{"curl", "curl", "--version"},
		{"wget", "wget", "--version"},
		{"jq", "jq", "--version"},
		{"rg", "rg", "--version"},
		{"docker", "docker", "--version"},
		{"podman", "podman", "--version"},
		{"make", "make", "--version"},
		{"uv", "uv", "--version"},
		{"pip", "pip3", "--version"},
		{"npm", "npm", "--version"},
	}

	for _, p := range probes {
		name, bin, flag := p[0], p[1], p[2]
		if _, err := exec.LookPath(bin); err != nil {
			continue
		}
		cmd := commandContext(ctx, bin, flag)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			continue
		}
		if ver := versionRe.FindString(out.String()); ver != "" {
			runtimes[name] = ver
		}
	}

	return runtimes
}

