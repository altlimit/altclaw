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
	Workspace    string     // host path to mount into container
	MountPath    string     // path inside container (default: /workspace)
	cli          string     // container runtime CLI binary ("docker" or "podman")
	prefix       string     // container name prefix, derived from workspace
	networkName  string     // dedicated internal network for this executor instance
	proxyRelay   string     // container ID of the proxy relay sidecar
	proxyRelayIP string     // IP of the relay container on the internal network
	allowedPorts  []string     // host ports the proxy should allow through (e.g. the altclaw server port)
	heartbeatDir  string       // host temp dir mounted into containers for idle detection
	heartbeatStop chan struct{} // stop channel for heartbeat goroutine
	infraReady   bool          // true once network + relay are up
	infraErr     error         // cached error from last infra attempt
	infraRetries int           // number of infra retry attempts
	infraMu      sync.Mutex // guards infra setup (separate from mu to avoid deadlock)

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
// Includes a short hash of the full path to avoid collisions when multiple
// instances use workspaces with the same basename.
// Example: /home/user/projects/myapp → "altclaw-myapp-a1b2c3d4"
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
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(workspace)))[:8]
	return "altclaw-" + clean + "-" + hash
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

// ensureInfra lazily creates the internal network and SSRF proxy relay
// container on first use. Thread-safe via mutex. Retries if a previous
// attempt failed.
func (d *Docker) ensureInfra() error {
	d.infraMu.Lock()
	defer d.infraMu.Unlock()

	if d.infraReady {
		return nil
	}
	if d.infraErr != nil {
		// Previous attempt failed — retry (max 3 total)
		d.infraRetries++
		if d.infraRetries > 3 {
			return d.infraErr
		}
		slog.Info("retrying docker infra setup after previous failure", "attempt", d.infraRetries, "error", d.infraErr)
		d.infraErr = nil
	}

	// Set up heartbeat dir for container idle detection.
	// The host process touches a file every 30s; containers monitor it
	// and self-terminate when it goes stale (survives hard kills).
	if d.heartbeatDir == "" {
		dir, err := os.MkdirTemp("", "altclaw-hb-*")
		if err != nil {
			slog.Warn("failed to create heartbeat dir, containers won't auto-stop", "err", err)
		} else {
			d.heartbeatDir = dir
			_ = os.WriteFile(filepath.Join(dir, "alive"), nil, 0644)
			d.heartbeatStop = make(chan struct{})
			go d.heartbeatLoop()
		}
	}

	d.networkName = fmt.Sprintf("%s-net", d.prefix)

	// Setup isolated internal network
	if err := d.setupNetwork(); err != nil {
		d.infraErr = err
		return err
	}

	// Start SSRF proxy relay sidecar
	if err := d.startProxyRelay(); err != nil {
		_ = command(d.cli, "network", "rm", d.networkName).Run()
		d.infraErr = err
		return err
	}

	d.infraReady = true
	return nil
}

// containerIdleTimeout is how long (seconds) containers wait after the last
// heartbeat before self-terminating. Survives hard kills of the host process.
const containerIdleTimeout = 60 // heartbeat is every 30s; only stops when process dies

// heartbeatLoop periodically touches the heartbeat file so containers know
// the host process is still alive.
func (d *Docker) heartbeatLoop() {
	hbFile := filepath.Join(d.heartbeatDir, "alive")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			_ = os.Chtimes(hbFile, now, now)
		case <-d.heartbeatStop:
			return
		}
	}
}

// setupNetwork creates an --internal Docker network.
// Internal networks block ALL external access, including to the host.
// App containers use the proxy relay sidecar (also on this network) for internet.
func (d *Docker) setupNetwork() error {
	_ = command(d.cli, "network", "rm", d.networkName).Run()

	var stderr bytes.Buffer
	cmd := command(d.cli, "network", "create", "--internal", d.networkName)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create docker network %s: %s", d.networkName, stderr.String())
	}
	return nil
}

// ensureProxyImage builds the Go-based SSRF proxy image from the embedded
// buildpack if it doesn't already exist. Tagged by content hash so it's
// only rebuilt when the Dockerfile changes.
func (d *Docker) ensureProxyImage() (string, error) {
	data, ok := GetBuildpack("proxy.Dockerfile")
	if !ok {
		return "", fmt.Errorf("proxy.Dockerfile buildpack not found")
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(data))[:12]
	tag := "altclaw/proxy:" + hash

	// Check if already built
	if err := command(d.cli, "image", "inspect", tag).Run(); err == nil {
		return tag, nil
	}

	slog.Info("building SSRF proxy image (first time only)", "tag", tag)
	cmd := command(d.cli, "build", "-t", tag, "-")
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker build proxy: %s: %w", stderr.String(), err)
	}
	slog.Info("proxy image built", "tag", tag)
	return tag, nil
}

// AllowPort whitelists a host port in the proxy relay so that app
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
		slog.Debug("restarting proxy relay for new allowed port", "port", port)

		if err := d.startProxyRelay(); err != nil {
			slog.Warn("failed to restart proxy relay after AllowPort", "port", port, "err", err)
		}
	}
}

// startProxyRelay runs a Go-based SSRF proxy container as the relay sidecar.
// The proxy blocks connections to private/loopback/metadata IPs.
// The container is connected to BOTH the internal network (so app containers
// can use it as a proxy) and the bridge network (for internet access via Docker NAT).
func (d *Docker) startProxyRelay() error {
	relayName := d.prefix + "-relay"

	// Clean up any stale relay container
	_ = command(d.cli, "rm", "-f", "-v", relayName).Run()

	// Build the Go-based SSRF proxy image (cached by content hash)
	proxyImage, err := d.ensureProxyImage()
	if err != nil {
		return fmt.Errorf("failed to build proxy image: %w", err)
	}

	// Pass allowed host ports via env var for SSRF whitelist
	d.mu.Lock()
	ports := append([]string(nil), d.allowedPorts...)
	d.mu.Unlock()
	allowedPorts := strings.Join(ports, ",")

	var stdout bytes.Buffer
	args := []string{"run", "-d",
		"--restart=no",
		"--name", relayName,
		"--network", "bridge",
	}
	if allowedPorts != "" {
		args = append(args, "-e", "ALLOWED_PORTS="+allowedPorts)
	}
	// Mount heartbeat dir so relay can detect host process death
	if d.heartbeatDir != "" {
		args = append(args, "-v", d.heartbeatDir+":/tmp/altclaw-hb:ro")
		args = append(args, "-e", "HEARTBEAT_FILE=/tmp/altclaw-hb/alive")
		args = append(args, "-e", fmt.Sprintf("IDLE_TIMEOUT=%d", containerIdleTimeout))
	}
	args = append(args, proxyImage)
	cmd := command(d.cli, args...)
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start proxy relay: %s", stderr.String())
	}
	d.proxyRelay = strings.TrimSpace(stdout.String())

	// Go proxy starts in milliseconds
	time.Sleep(500 * time.Millisecond)

	// Verify the relay container is still running
	var stateOut bytes.Buffer
	stateCmd := command(d.cli, "inspect", "-f", "{{.State.Running}}", relayName)
	stateCmd.Stdout = &stateOut
	if err := stateCmd.Run(); err == nil && strings.TrimSpace(stateOut.String()) != "true" {
		var logsOut bytes.Buffer
		logsCmd := command(d.cli, "logs", "--tail", "20", relayName)
		logsCmd.Stdout = &logsOut
		logsCmd.Stderr = &logsOut
		_ = logsCmd.Run()
		d.stopContainer(d.proxyRelay)
		return fmt.Errorf("proxy relay container exited unexpectedly: %s", strings.TrimSpace(logsOut.String()))
	}

	// Connect the relay container to the internal network
	connCmd := command(d.cli, "network", "connect", d.networkName, relayName)
	var connErr bytes.Buffer
	connCmd.Stderr = &connErr
	if err := connCmd.Run(); err != nil {
		d.stopContainer(d.proxyRelay)
		return fmt.Errorf("failed to connect relay to internal network: %s", connErr.String())
	}

	// Get the relay container's IP on the internal network.
	// Use `network inspect` (more reliable than container inspect on some Docker configs).
	var relayIP string
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Second)
		}
		ip, err := d.getContainerIPOnNetwork(d.networkName, relayName)
		if err != nil {
			slog.Warn("relay IP lookup failed", "attempt", attempt+1, "error", err)
			continue
		}
		if ip != "" {
			relayIP = ip
			break
		}
		slog.Warn("relay IP empty, retrying", "attempt", attempt+1)
	}

	if relayIP == "" {
		d.stopContainer(d.proxyRelay)
		return fmt.Errorf("relay container has no IP on internal network %s", d.networkName)
	}
	d.proxyRelayIP = relayIP
	slog.Info("relay proxy ready", "ip", relayIP, "network", d.networkName)

	return nil
}

// getContainerIPOnNetwork queries the network to find a container's IP.
// Tries multiple methods for Docker/Podman compatibility.
func (d *Docker) getContainerIPOnNetwork(network, containerName string) (string, error) {
	// Method 1: network inspect with Go template (Docker-style)
	// This doesn't work on Podman 4.x (no .Containers field), so failures fall through.
	var stdout bytes.Buffer
	cmd := command(d.cli, "network", "inspect", network, "--format",
		`{{range $id, $c := .Containers}}{{$c.Name}}={{$c.IPv4Address}} {{end}}`)
	cmd.Stdout = &stdout
	if err := cmd.Run(); err == nil {
		// Parse "name=172.18.0.2/16 name2=172.18.0.3/16"
		for _, entry := range strings.Fields(stdout.String()) {
			parts := strings.SplitN(entry, "=", 2)
			if len(parts) == 2 && parts[0] == containerName {
				ip := parts[1]
				// Strip CIDR suffix (e.g. "172.18.0.2/16" → "172.18.0.2")
				if idx := strings.Index(ip, "/"); idx > 0 {
					ip = ip[:idx]
				}
				return ip, nil
			}
		}
	}

	// Method 2: container inspect with specific network (works on both Docker and Podman)
	var ipOut bytes.Buffer
	ipCmd := command(d.cli, "inspect", "-f",
		fmt.Sprintf(`{{(index .NetworkSettings.Networks "%s").IPAddress}}`, network),
		containerName,
	)
	ipCmd.Stdout = &ipOut
	if err := ipCmd.Run(); err != nil {
		return "", fmt.Errorf("container inspect: %w", err)
	}
	ip := strings.TrimSpace(ipOut.String())
	if ip == "<no value>" {
		ip = ""
	}
	return ip, nil
}


// cleanupStale removes any Docker containers with our naming prefix
// that may be left over from a previous run (e.g. crash, kill -9).
func (d *Docker) cleanupStale() {
	var stdout bytes.Buffer
	cmd := command(d.cli, "ps", "-a", "-q",
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
	// Mount heartbeat dir so containers detect host process death
	if d.heartbeatDir != "" {
		args = append(args, "-v", d.heartbeatDir+":/tmp/altclaw-hb:ro")
	}
	// Add extra volumes from SetImage opts
	d.mu.Lock()
	for _, v := range d.extraVolumes {
		args = append(args, "-v", v)
	}
	d.mu.Unlock()
	// Use idle-detecting watchdog instead of sleep infinity.
	// Checks heartbeat file mtime; exits when stale (host process dead).
	// Falls back to sleep infinity when no heartbeat dir is available.
	if d.heartbeatDir != "" {
		watchdog := fmt.Sprintf(
			`while true; do sleep 30; [ ! -f /tmp/altclaw-hb/alive ] && exit 0; now=$(date +%%s); mod=$(stat -c %%Y /tmp/altclaw-hb/alive 2>/dev/null || echo $now); [ $(( now - mod )) -gt %d ] && exit 0; done`,
			containerIdleTimeout)
		args = append(args, "-w", d.MountPath, image, "sh", "-c", watchdog)
	} else {
		args = append(args, "-w", d.MountPath, image, "sleep", "infinity")
	}
	cmd := commandContext(ctx, d.cli, args...)
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return "", fmt.Errorf("docker run (%s): %s: %w", image, detail, err)
		}
		return "", fmt.Errorf("docker run (%s): %w", image, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// stopContainer stops and removes a container.
func (d *Docker) stopContainer(containerID string) {
	_ = command(d.cli, "rm", "-f", "-v", containerID).Run()
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

	c := commandContext(ctx, d.cli, dockerArgs...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err = c.Run()
	exitCode := 0
	if err != nil {
		// Context cancellation/timeout should surface as an error, not a non-zero exit code
		if ctx.Err() != nil {
			return nil, fmt.Errorf("docker exec: %w", ctx.Err())
		}
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

	c := commandContext(ctx, d.cli, dockerArgs...)
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
	slog.Debug("docker executor cleanup starting")
	d.mu.Lock()

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
		_ = command(d.cli, "network", "rm", d.networkName).Run()
	}

	// Stop heartbeat goroutine and remove heartbeat dir.
	// Removing the dir causes containers to detect the missing file and exit.
	if d.heartbeatStop != nil {
		close(d.heartbeatStop)
		d.heartbeatStop = nil
	}
	if d.heartbeatDir != "" {
		_ = os.RemoveAll(d.heartbeatDir)
		d.heartbeatDir = ""
	}

	d.infraReady = false
	d.mu.Unlock()

	// Belt-and-suspenders: also clean by prefix in case any containers
	// were missed (e.g. created between the ID check and now).
	d.cleanupStale()

	slog.Debug("docker executor cleanup done")
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
	if err := command(d.cli, "image", "inspect", tag).Run(); err == nil {
		slog.Debug("image already exists, skipping build", "tag", tag)
		return tag, nil
	}

	// Build the image
	slog.Info("building Docker image", "tag", tag, "source", build)
	cmd := command(d.cli, "build", "-t", tag, "-")
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

	c := commandContext(ctx, d.cli, dockerArgs...)

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
