package executor

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Docker implements Executor by shelling out to the docker CLI.
// Supports per-session containers: sub-agents can use different images
// without affecting the main agent's container.
//
// Containers are named with a prefix derived from the workspace path
// (e.g. "altclaw-myproject-<uuid>") so stale containers can be cleaned
// up on startup.
type Docker struct {
	Image        string
	Workspace    string    // host path to mount into container
	MountPath    string    // path inside container (default: /workspace)
	cli          string    // container runtime CLI binary ("docker" or "podman")
	prefix       string    // container name prefix, derived from workspace
	networkName  string    // dedicated internal network for this executor instance
	proxyRelay   string    // container ID of the proxy relay sidecar
	proxyRelayIP string    // IP of the relay container on the internal network
	allowedPorts []string  // host ports squid should allow through (e.g. the altclaw server port)
	infraOnce    sync.Once // ensures network/relay are created once

	mu               sync.Mutex
	defaultContainer string                           // main agent's container
	sessions         map[string]string                // sessionID → containerID
	spawned          map[string]*spawnedDockerProcess // handleID → spawned process
	popened          map[string]*popenDockerProcess   // handleID → popen'd process
	extraVolumes     []string                         // extra volume mounts from SetImage opts
}

// sanitize strips non-alphanumeric characters for use in Docker container names.
var sanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// qualifyImage ensures an image reference is fully qualified (includes registry).
// Docker auto-resolves short names like "alpine:latest" but Podman requires
// fully qualified names like "docker.io/library/alpine:latest".
func qualifyImage(image string) string {
	// Strip tag/digest before checking for registry prefix
	name := image
	if i := strings.LastIndex(name, ":"); i > 0 {
		name = name[:i]
	}
	// Already qualified (contains a dot in the first segment = registry domain)
	parts := strings.SplitN(name, "/", 2)
	if strings.Contains(parts[0], ".") {
		return image
	}
	// Single name like "alpine:latest" → docker.io/library/alpine:latest
	if len(parts) == 1 {
		return "docker.io/library/" + image
	}
	// Two-part like "alpine/socat" → docker.io/alpine/socat
	return "docker.io/" + image
}

// containerPrefix returns a deterministic prefix from the workspace path.
// Example: /home/user/projects/myapp → "altclaw-myapp"
func containerPrefix(workspace string) string {
	base := filepath.Base(workspace)
	if base == "" || base == "." || base == "/" {
		base = "default"
	}
	clean := sanitizeRe.ReplaceAllString(strings.ToLower(base), "-")
	clean = strings.Trim(clean, "-")
	if clean == "" {
		clean = "default"
	}
	return "altclaw-" + clean
}

// NewDocker creates a Docker/Podman executor with the given image and workspace mount.
// cli is the container runtime binary name ("docker" or "podman").
// Infrastructure (proxy, network, relay) is created lazily on first use.
func NewDocker(cli, image, workspace, mountPath string) (*Docker, error) {
	if mountPath == "" {
		mountPath = "/workspace"
	}
	if cli == "" {
		cli = "docker"
	}
	// Verify CLI is available
	if _, err := exec.LookPath(cli); err != nil {
		return nil, fmt.Errorf("%s CLI not found: %w", cli, err)
	}

	prefix := containerPrefix(workspace)

	d := &Docker{
		Image:     qualifyImage(image),
		Workspace: workspace,
		MountPath: mountPath,
		cli:       cli,
		prefix:    prefix,
		sessions:  make(map[string]string),
		spawned:   make(map[string]*spawnedDockerProcess),
	}

	// Clean up stale containers from a previous run
	d.cleanupStale()

	return d, nil
}

// ensureInfra lazily creates the internal network and squid proxy relay
// container on first use. Thread-safe via sync.Once.
func (d *Docker) ensureInfra() error {
	var initErr error
	d.infraOnce.Do(func() {
		d.networkName = fmt.Sprintf("%s-net", d.prefix)

		// Setup isolated internal network
		if err := d.setupNetwork(); err != nil {
			initErr = err
			return
		}

		// Start squid proxy relay sidecar
		if err := d.startProxyRelay(); err != nil {
			_ = exec.Command(d.cli, "network", "rm", d.networkName).Run()
			initErr = err
			return
		}
	})
	return initErr
}

// setupNetwork creates an --internal Docker network.
// Internal networks block ALL external access, including to the host.
// App containers use the proxy relay sidecar (also on this network) for internet.
func (d *Docker) setupNetwork() error {
	_ = exec.Command(d.cli, "network", "rm", d.networkName).Run()

	var stderr bytes.Buffer
	cmd := exec.Command(d.cli, "network", "create", "--internal", d.networkName)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create docker network %s: %s", d.networkName, stderr.String())
	}
	return nil
}

// buildSquidConf returns a squid.conf string with SSRF-protective ACLs.
// allowedPorts contains host ports that should bypass the private-IP block
// (e.g. the altclaw web server port on the bridge gateway).
func (d *Docker) buildSquidConf() string {
	var sb strings.Builder
	sb.WriteString("http_port 3128\n")
	sb.WriteString("# Allow only safe ports\n")
	sb.WriteString("acl safe_ports port 80 443 8080 8443")

	// Add any explicitly allowed host ports to the safe_ports ACL
	d.mu.Lock()
	ports := append([]string(nil), d.allowedPorts...)
	d.mu.Unlock()
	for _, p := range ports {
		sb.WriteString(" " + p)
	}
	sb.WriteString("\nhttp_access deny !safe_ports\n")

	// Allow connections to the host bridge gateway on whitelisted ports.
	// These rules MUST come before the private-network deny rules.
	if len(ports) > 0 {
		hostIP := d.getHostIP()
		sb.WriteString("# Allow host (altclaw server) on whitelisted ports\n")
		sb.WriteString("acl altclaw_host dst " + hostIP + "\n")
		sb.WriteString("acl altclaw_ports port")
		for _, p := range ports {
			sb.WriteString(" " + p)
		}
		sb.WriteString("\nhttp_access allow altclaw_host altclaw_ports\n")
	}

	sb.WriteString("# Block private/loopback/cloud metadata IPs (SSRF protection)\n")
	sb.WriteString("acl loopback dst 127.0.0.0/8\n")
	sb.WriteString("acl private dst 10.0.0.0/8\n")
	sb.WriteString("acl private dst 172.16.0.0/12\n")
	sb.WriteString("acl private dst 192.168.0.0/16\n")
	sb.WriteString("acl link_local dst 169.254.0.0/16\n")
	sb.WriteString("acl metadata dst 169.254.169.254/32\n")
	sb.WriteString("http_access deny loopback\n")
	sb.WriteString("http_access deny private\n")
	sb.WriteString("http_access deny link_local\n")
	sb.WriteString("http_access deny metadata\n")
	sb.WriteString("http_access allow all\n")
	sb.WriteString("cache deny all\n")
	sb.WriteString("access_log none\n")
	sb.WriteString("cache_log /dev/null\n")
	return sb.String()
}

// AllowPort whitelists a host port in the squid proxy relay so that app
// containers can reach the altclaw server on that port.
// If the relay is already running, it restarts with the updated config.
func (d *Docker) AllowPort(port string) {
	d.mu.Lock()
	for _, p := range d.allowedPorts {
		if p == port {
			d.mu.Unlock()
			return // already allowed
		}
	}
	d.allowedPorts = append(d.allowedPorts, port)
	relayRunning := d.proxyRelay != ""
	d.mu.Unlock()

	if relayRunning {
		// Restart relay with updated config
		slog.Debug("restarting squid relay for new allowed port", "port", port)
		if err := d.startProxyRelay(); err != nil {
			slog.Warn("failed to restart proxy relay after AllowPort", "port", port, "err", err)
		}
	}
}

// startProxyRelay runs a squid HTTP proxy container as the relay sidecar.
// Squid enforces SSRF rules via ACLs (blocks private/loopback/metadata IPs).
// The container is connected to BOTH the internal network (so app containers
// can use it as a proxy) and the bridge network (for internet access via Docker NAT).
// This design works on WSL2 where the host Go process cannot make outbound
// internet connections directly — outbound connections happen inside the container.
func (d *Docker) startProxyRelay() error {
	relayName := d.prefix + "-relay"

	// Clean up any stale relay container
	_ = exec.Command(d.cli, "rm", "-f", "-v", relayName).Run()

	squidConf := d.buildSquidConf()

	var stdout bytes.Buffer
	cmd := exec.Command(d.cli, "run", "-d",
		"--restart=no",
		"--name", relayName,
		"--network", "bridge",
		"-e", "SQUID_CONF="+squidConf,
		"--entrypoint", "sh",
		"docker.io/ubuntu/squid",
		"-c", `echo "$SQUID_CONF" > /etc/squid/squid.conf && squid -N -f /etc/squid/squid.conf`,
	)
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start squid relay: %s", stderr.String())
	}
	d.proxyRelay = strings.TrimSpace(stdout.String())

	// Give squid a moment to start
	time.Sleep(2 * time.Second)

	// Connect the relay container to the internal network
	connCmd := exec.Command(d.cli, "network", "connect", d.networkName, relayName)
	var connErr bytes.Buffer
	connCmd.Stderr = &connErr
	if err := connCmd.Run(); err != nil {
		d.stopContainer(d.proxyRelay)
		return fmt.Errorf("failed to connect relay to internal network: %s", connErr.String())
	}

	// Get the relay container's IP on the internal network
	var ipOut bytes.Buffer
	ipCmd := exec.Command(d.cli, "inspect", "-f",
		fmt.Sprintf(`{{(index .NetworkSettings.Networks "%s").IPAddress}}`, d.networkName),
		relayName,
	)
	ipCmd.Stdout = &ipOut
	if err := ipCmd.Run(); err != nil {
		d.stopContainer(d.proxyRelay)
		return fmt.Errorf("failed to get relay IP: %w", err)
	}
	d.proxyRelayIP = strings.TrimSpace(ipOut.String())
	if d.proxyRelayIP == "" {
		d.stopContainer(d.proxyRelay)
		return fmt.Errorf("relay container has no IP on internal network")
	}

	return nil
}

// getHostIP returns the host IP as seen from the default bridge network.
func (d *Docker) getHostIP() string {
	var stdout bytes.Buffer
	cmd := exec.Command(d.cli, "network", "inspect", "bridge", "-f", "{{(index .IPAM.Config 0).Gateway}}")
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "172.17.0.1"
	}
	ip := strings.TrimSpace(stdout.String())
	if ip == "" {
		return "172.17.0.1"
	}
	return ip
}

// getGatewayIP gets the gateway IP of the created network
func (d *Docker) getGatewayIP() string {
	var stdout bytes.Buffer
	cmd := exec.Command(d.cli, "network", "inspect", d.networkName, "-f", "{{(index .IPAM.Config 0).Gateway}}")
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		// Fallback to default bridge gateway if something fails
		return "172.17.0.1"
	}
	ip := strings.TrimSpace(stdout.String())
	if ip == "" {
		return "172.17.0.1"
	}
	return ip
}

// cleanupStale removes any Docker containers with our naming prefix
// that may be left over from a previous run (e.g. crash, kill -9).
func (d *Docker) cleanupStale() {
	var stdout bytes.Buffer
	cmd := exec.Command(d.cli, "ps", "-a", "-q",
		"--filter", "name=^"+d.prefix+"-",
	)
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return
	}
	ids := strings.TrimSpace(stdout.String())
	if ids == "" {
		return
	}
	for _, id := range strings.Split(ids, "\n") {
		id = strings.TrimSpace(id)
		if id != "" {
			d.stopContainer(id)
		}
	}
}

// containerName generates a unique container name with our prefix.
func (d *Docker) containerName() string {
	short := uuid.New().String()[:8]
	return d.prefix + "-" + short
}

// spawnContainer creates a new Docker container with the given image.
// Uses --restart=no and a named prefix for cleanup identification.
func (d *Docker) spawnContainer(ctx context.Context, image string) (string, error) {
	name := d.containerName()

	// Use the relay container's IP on the internal network as the proxy
	proxyURL := fmt.Sprintf("http://%s:3128", d.proxyRelayIP)

	var stdout bytes.Buffer
	args := []string{"run", "-d",
		"--restart=no",
		"--name", name,
		"--network", d.networkName,
		"-e", "HTTP_PROXY=" + proxyURL,
		"-e", "HTTPS_PROXY=" + proxyURL,
		"-e", "http_proxy=" + proxyURL,
		"-e", "https_proxy=" + proxyURL,
		"-e", "NO_PROXY=localhost,127.0.0.1",
		"-e", "no_proxy=localhost,127.0.0.1",
		"-v", d.Workspace + ":" + d.MountPath,
	}
	// Add extra volumes from SetImage opts
	d.mu.Lock()
	for _, v := range d.extraVolumes {
		args = append(args, "-v", v)
	}
	d.mu.Unlock()
	args = append(args, "-w", d.MountPath, image, "sleep", "infinity")
	cmd := exec.CommandContext(ctx, d.cli, args...)
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker run (%s): %w", image, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// stopContainer stops and removes a container.
func (d *Docker) stopContainer(containerID string) {
	_ = exec.Command(d.cli, "rm", "-f", "-v", containerID).Run()
}

// containerForSession returns the container ID to use for the given context.
// If the session has its own container, use that; otherwise use default.
func (d *Docker) containerForSession(ctx context.Context) (string, error) {
	// Lazily initialize infrastructure on first use
	if err := d.ensureInfra(); err != nil {
		return "", err
	}

	sessionID := SessionFrom(ctx)

	d.mu.Lock()
	if sessionID != "" {
		if cid, ok := d.sessions[sessionID]; ok {
			d.mu.Unlock()
			return cid, nil
		}
	}
	d.mu.Unlock()

	// Use or create default container
	if d.defaultContainer == "" {
		cid, err := d.spawnContainer(ctx, d.Image)
		if err != nil {
			return "", err
		}
		d.mu.Lock()
		d.defaultContainer = cid
		d.mu.Unlock()
	}
	return d.defaultContainer, nil
}

// Run executes a command inside the Docker container synchronously.
func (d *Docker) Run(ctx context.Context, cmd string, args []string) (*Result, error) {
	containerID, err := d.containerForSession(ctx)
	if err != nil {
		return nil, err
	}

	dockerArgs := []string{"exec"}
	envMap := EnvFrom(ctx)
	for k, v := range envMap {
		dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	dockerArgs = append(dockerArgs, containerID, cmd)
	dockerArgs = append(dockerArgs, args...)

	c := exec.CommandContext(ctx, d.cli, dockerArgs...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err = c.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("docker exec: %w", err)
		}
	}

	return &Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}

// spawnedDockerProcess tracks a spawned docker exec process with output buffers.
type spawnedDockerProcess struct {
	cmd    *exec.Cmd
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

// popenDockerProcess tracks an interactive docker exec -i process.
type popenDockerProcess struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan string
	err   error
	done  chan struct{}
}

// Spawn starts a command asynchronously inside the container.
func (d *Docker) Spawn(ctx context.Context, cmd string, args []string) (string, error) {
	containerID, err := d.containerForSession(ctx)
	if err != nil {
		return "", err
	}

	dockerArgs := []string{"exec"}
	envMap := EnvFrom(ctx)
	for k, v := range envMap {
		dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	dockerArgs = append(dockerArgs, containerID, cmd)
	dockerArgs = append(dockerArgs, args...)

	c := exec.CommandContext(ctx, d.cli, dockerArgs...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	if err := c.Start(); err != nil {
		return "", fmt.Errorf("docker exec start: %w", err)
	}

	id := uuid.New().String()
	d.mu.Lock()
	d.spawned[id] = &spawnedDockerProcess{cmd: c, stdout: &stdout, stderr: &stderr}
	d.mu.Unlock()

	go func() { _ = c.Wait() }()

	return id, nil
}

// GetOutput retrieves stdout from a spawned process.
func (d *Docker) GetOutput(handleID string) (string, error) {
	d.mu.Lock()
	sp, ok := d.spawned[handleID]
	d.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("unknown handle %q", handleID)
	}
	return sp.stdout.String(), nil
}

// Terminate kills a spawned or popen'd process.
func (d *Docker) Terminate(handleID string) error {
	d.mu.Lock()
	sp, ok := d.spawned[handleID]
	pp, okP := d.popened[handleID]
	d.mu.Unlock()
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

// Cleanup stops and removes all containers (default + sessions).
func (d *Docker) Cleanup() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Kill popen'd processes
	for id, pp := range d.popened {
		if pp.stdin != nil {
			pp.stdin.Close()
		}
		if pp.cmd.Process != nil {
			_ = pp.cmd.Process.Kill()
		}
		delete(d.popened, id)
	}

	if d.defaultContainer != "" {
		d.stopContainer(d.defaultContainer)
		d.defaultContainer = ""
	}
	for sid, cid := range d.sessions {
		d.stopContainer(cid)
		delete(d.sessions, sid)
	}

	// Stop proxy relay container
	if d.proxyRelay != "" {
		d.stopContainer(d.proxyRelay)
		d.proxyRelay = ""
	}

	// Remove isolated network
	if d.networkName != "" {
		_ = exec.Command(d.cli, "network", "rm", d.networkName).Run()
	}

	return nil
}

// SetImage changes the Docker image.
//   - sessionID "": switches default container (stops old, next cmd spawns new)
//   - sessionID != "": spawns a new container for that session only
//
// If opts.Build is set, the image is built from the Dockerfile first.
func (d *Docker) SetImage(image string, opts ImageOpts, sessionID string) {
	// Resolve and build if Dockerfile specified
	resolvedImage := image
	if opts.Build != "" {
		built, err := d.resolveAndBuild(image, opts.Build)
		if err != nil {
			slog.Error("setImage build failed", "image", image, "build", opts.Build, "error", err)
			return
		}
		resolvedImage = built
	}

	// Store extra volumes for future container spawns
	if len(opts.Volumes) > 0 {
		d.mu.Lock()
		d.extraVolumes = opts.Volumes
		d.mu.Unlock()
	}

	if sessionID == "" {
		// Main agent: switch default
		d.mu.Lock()
		if d.defaultContainer != "" {
			d.stopContainer(d.defaultContainer)
			d.defaultContainer = ""
		}
		d.Image = qualifyImage(resolvedImage)
		d.mu.Unlock()
		return
	}

	// Sub-agent: spawn a session-specific container
	cid, err := d.spawnContainer(context.Background(), qualifyImage(resolvedImage))
	if err != nil {
		return // silently fail; sys.call will fail with default container
	}
	d.mu.Lock()
	d.sessions[sessionID] = cid
	d.mu.Unlock()
}

// resolveAndBuild resolves a Dockerfile source and builds the image if needed.
// Returns the final image tag (e.g. "altclaw/mcp:a1b2c3d4e5f6").
func (d *Docker) resolveAndBuild(image, build string) (string, error) {
	var content []byte

	if strings.HasPrefix(build, "./") || strings.HasPrefix(build, "/") {
		// Workspace Dockerfile
		path := build
		if strings.HasPrefix(build, "./") {
			path = filepath.Join(d.Workspace, build[2:])
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read workspace Dockerfile %q: %w", build, err)
		}
		content = data
	} else {
		// Embedded buildpack
		data, ok := GetBuildpack(build)
		if !ok {
			return "", fmt.Errorf("buildpack %q not found (available: %v)", build, ListBuildpacks())
		}
		content = data
	}

	// Tag based on content hash — rebuild only when Dockerfile changes
	hash := fmt.Sprintf("%x", sha256.Sum256(content))[:12]
	tag := image + ":" + hash

	// Check if image already exists
	if err := exec.Command(d.cli, "image", "inspect", tag).Run(); err == nil {
		slog.Debug("image already exists, skipping build", "tag", tag)
		return tag, nil
	}

	// Build the image
	slog.Info("building Docker image", "tag", tag, "source", build)
	cmd := exec.Command(d.cli, "build", "-t", tag, "-")
	cmd.Stdin = bytes.NewReader(content)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker build %q: %s: %w", tag, stderr.String(), err)
	}
	slog.Info("image built successfully", "tag", tag)
	return tag, nil
}

// CleanupSession removes a session-specific container.
func (d *Docker) CleanupSession(sessionID string) {
	d.mu.Lock()
	cid, ok := d.sessions[sessionID]
	if ok {
		delete(d.sessions, sessionID)
	}
	d.mu.Unlock()

	if ok {
		d.stopContainer(cid)
	}
}

// Popen starts a command with stdin piped and stdout line-buffered inside Docker.
// Uses "docker exec -i" to keep stdin open.
func (d *Docker) Popen(ctx context.Context, cmd string, args []string) (string, error) {
	containerID, err := d.containerForSession(ctx)
	if err != nil {
		return "", err
	}

	dockerArgs := []string{"exec", "-i"}
	envMap := EnvFrom(ctx)
	for k, v := range envMap {
		dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	dockerArgs = append(dockerArgs, containerID, cmd)
	dockerArgs = append(dockerArgs, args...)

	c := exec.CommandContext(ctx, d.cli, dockerArgs...)

	stdinPipe, err := c.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("docker popen stdin pipe: %w", err)
	}

	stdoutPipe, err := c.StdoutPipe()
	if err != nil {
		stdinPipe.Close()
		return "", fmt.Errorf("docker popen stdout pipe: %w", err)
	}

	if err := c.Start(); err != nil {
		stdinPipe.Close()
		return "", fmt.Errorf("docker popen start: %w", err)
	}

	id := uuid.New().String()
	pp := &popenDockerProcess{
		cmd:   c,
		stdin: stdinPipe,
		lines: make(chan string, 64),
		done:  make(chan struct{}),
	}

	// Goroutine reads stdout line by line
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimRight(scanner.Text(), "\r")
			line = ansiRe.ReplaceAllString(line, "") // strip ANSI escape codes
			pp.lines <- line
		}
		pp.err = scanner.Err()
		close(pp.done)
	}()

	d.mu.Lock()
	if d.popened == nil {
		d.popened = make(map[string]*popenDockerProcess)
	}
	d.popened[id] = pp
	d.mu.Unlock()

	return id, nil
}

// WriteStdin writes data to the stdin of a Popen'd Docker process.
func (d *Docker) WriteStdin(handleID string, data string) error {
	d.mu.Lock()
	pp, ok := d.popened[handleID]
	d.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown popen handle %q", handleID)
	}
	_, err := io.WriteString(pp.stdin, data)
	return err
}

// ReadLine reads the next line from stdout of a Popen'd Docker process.
func (d *Docker) ReadLine(handleID string, timeoutMs int) (string, error) {
	d.mu.Lock()
	pp, ok := d.popened[handleID]
	d.mu.Unlock()
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

// Info returns environment introspection data from inside the Docker container.
// Runs a single shell script that gathers OS, resource, and runtime info.
func (d *Docker) Info(ctx context.Context) (map[string]any, error) {
	// Shell script that collects all info as key=value pairs.
	// Designed to work on minimal containers (alpine, debian, ubuntu).
	script := `#!/bin/sh
# OS info
[ -f /etc/os-release ] && . /etc/os-release
echo "OS_TYPE=linux"
echo "OS_DISTRO=${ID:-unknown}"
echo "OS_VERSION=${VERSION_ID:-unknown}"
echo "OS_ARCH=$(uname -m)"
echo "OS_KERNEL=$(uname -r)"

# Resources
echo "RES_CPUS=$(nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo 2>/dev/null || echo 1)"
if [ -f /proc/meminfo ]; then
  mem_kb=$(awk '/MemTotal/ {print $2}' /proc/meminfo)
  echo "RES_MEM_MB=$((mem_kb / 1024))"
fi
if command -v df >/dev/null 2>&1; then
  disk_kb=$(df -k / 2>/dev/null | awk 'NR==2 {print $4}')
  [ -n "$disk_kb" ] && echo "RES_DISK_MB=$((disk_kb / 1024))"
fi

# Runtimes — probe via a helper function that extracts the first semver-like match
ver() { "$1" $2 2>&1 | head -1 | grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1; }
for pair in "node:--version" "bun:--version" "python3:--version" "git:--version" "go:version" \
            "ffmpeg:-version" "ruby:--version" "java:-version" "php:--version" \
            "rustc:--version" "cargo:--version" "curl:--version" "wget:--version" \
            "jq:--version" "rg:--version" "docker:--version" "podman:--version" \
            "make:--version" "uv:--version" "pip3:--version" "npm:--version"; do
  cmd="${pair%%:*}"; flag="${pair#*:}"
  # Map binary name to label (rustc→rust, python3→python, pip3→pip)
  label="$cmd"
  case "$cmd" in rustc) label=rust;; python3) label=python;; pip3) label=pip;; esac
  command -v "$cmd" >/dev/null 2>&1 && {
    v=$(ver "$cmd" "$flag")
    [ -n "$v" ] && echo "RT_${label}=${v}"
  }
done

# Paths
echo "PATH_HOME=${HOME:-/root}"
`

	result, err := d.Run(ctx, "sh", []string{"-c", script})
	if err != nil {
		return nil, fmt.Errorf("sys.info: %w", err)
	}

	// Parse key=value output
	kv := make(map[string]string)
	for _, line := range strings.Split(result.Stdout, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			kv[k] = v
		}
	}

	osInfo := map[string]any{
		"type": kv["OS_TYPE"],
		"arch": kv["OS_ARCH"],
	}
	if v := kv["OS_DISTRO"]; v != "" && v != "unknown" {
		osInfo["distro"] = v
	}
	if v := kv["OS_VERSION"]; v != "" && v != "unknown" {
		osInfo["version"] = v
	}
	if v := kv["OS_KERNEL"]; v != "" {
		osInfo["kernel"] = v
	}

	resources := map[string]any{}
	if v := kv["RES_CPUS"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			resources["cpus"] = n
		}
	}
	if v := kv["RES_MEM_MB"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			resources["memory_total_mb"] = n
		}
	}
	if v := kv["RES_DISK_MB"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			resources["disk_free_mb"] = n
		}
	}

	runtimes := map[string]any{}
	for k, v := range kv {
		if strings.HasPrefix(k, "RT_") && v != "" {
			runtimes[strings.TrimPrefix(k, "RT_")] = v
		}
	}

	capabilities := map[string]any{
		"internet_access": true,
		"executor":        "docker",
	}

	paths := map[string]any{
		"workspace": d.MountPath,
	}
	if v := kv["PATH_HOME"]; v != "" {
		paths["home"] = v
	}

	return map[string]any{
		"os":           osInfo,
		"resources":    resources,
		"runtimes":     runtimes,
		"capabilities": capabilities,
		"paths":        paths,
	}, nil
}

