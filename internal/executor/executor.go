// Package executor defines the Executor interface for running commands
// on different backends (local, Docker, etc.)
package executor

import "context"

// sessionKey is the context key for passing session IDs.
type sessionKey struct{}

// WithSession returns a context with the given session ID.
func WithSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionKey{}, sessionID)
}

// SessionFrom extracts the session ID from a context. Returns "" if not set.
func SessionFrom(ctx context.Context) string {
	if v, ok := ctx.Value(sessionKey{}).(string); ok {
		return v
	}
	return ""
}

// envKey is the context key for passing extra environment variables for command execution.
type envKey struct{}

// WithEnv returns a context with the given environment map.
func WithEnv(ctx context.Context, env map[string]string) context.Context {
	return context.WithValue(ctx, envKey{}, env)
}

// EnvFrom extracts the environment map from a context.
func EnvFrom(ctx context.Context) map[string]string {
	if v, ok := ctx.Value(envKey{}).(map[string]string); ok {
		return v
	}
	return nil
}

// Result holds the output of a synchronous command execution.
type Result struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

// ImageOpts configures Docker image building for SetImage.
type ImageOpts struct {
	Build   string   // Dockerfile: embedded buildpack name or workspace path (starts with "./")
	Volumes []string // Extra volume mounts, e.g. ["altclaw-pkg-cache:/root/.npm"]
}

// Executor is the interface for command execution backends.
type Executor interface {
	// Run executes a command synchronously and returns the result.
	// Uses session ID from context to route to the correct container.
	Run(ctx context.Context, cmd string, args []string) (*Result, error)

	// Spawn starts a command asynchronously and returns a handle ID.
	Spawn(ctx context.Context, cmd string, args []string) (string, error)

	// GetOutput retrieves the current stdout buffer for a spawned process.
	GetOutput(handleID string) (string, error)

	// Terminate kills a spawned process by handle ID.
	Terminate(handleID string) error

	// Cleanup kills all processes and containers associated with this executor.
	Cleanup() error

	// SetImage changes the base image. Behavior depends on session:
	//   - sessionID "": switches the default container (stops old one)
	//   - sessionID "sub1_...": spawns a new container for that session only
	// If opts.Build is set, the image is built from the Dockerfile first.
	SetImage(image string, opts ImageOpts, sessionID string)

	// CleanupSession removes a session-specific container.
	// No-op if session has no container.
	CleanupSession(sessionID string)

	// Popen starts a command with stdin piped and stdout line-buffered.
	// Returns a handle ID for use with WriteStdin/ReadLine.
	Popen(ctx context.Context, cmd string, args []string) (string, error)

	// WriteStdin writes data to the stdin of a Popen'd process.
	WriteStdin(handleID string, data string) error

	// ReadLine reads the next line from stdout of a Popen'd process.
	// Blocks until a line is available, the process exits, or timeout.
	ReadLine(handleID string, timeoutMs int) (string, error)

	// Info returns environment introspection data: OS, resources, runtimes,
	// capabilities, and paths. Used by sys.info() bridge.
	Info(ctx context.Context) (map[string]any, error)
}
