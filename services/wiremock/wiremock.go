// Package wiremock provides a WireMock service backed by Testcontainers.
// The exported WireMock type implements testrig.Service.
package wiremock

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	dockerclient "github.com/moby/moby/client"
	"github.com/sha1n/testrig"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/wiremock/go-wiremock"
)

// Tunables for the verbose log supervisor.
const (
	// logRestartBackoff is the pause between successive ContainerLogs calls
	// after a clean EOF. Keeps the supervisor from spinning if Docker keeps
	// returning streams that immediately close (e.g. container gone).
	logRestartBackoff = 500 * time.Millisecond

	// logStopWait is the maximum time Stop waits for the log goroutine to
	// finish after cancelling its context and terminating the container.
	logStopWait = 5 * time.Second
)

const (
	defaultImage = "wiremock/wiremock"
	defaultTag   = "3.2.0"

	// containerPort is the port WireMock listens on inside the container.
	containerPort = "8080"
)

// WireMock is a pre-configured WireMock test harness. It implements
// testrig.Service so it can be added to a testrig.Env, and exposes typed-client
// accessors (URL, Client) usable once the env has started.
//
// Construct with New, configure via the With* methods (chainable), then pass
// to env.With(...). A WireMock instance is reusable across Start/Stop cycles:
// Stop releases the container so a subsequent Start builds a fresh one.
// Calling Start without Stop in between returns an error.
//
// The service URL is published under "<name>.url" by default; override the
// key via WithURLPropertyName so tests can wire it directly into the
// application's expected config key.
type WireMock struct {
	name   string
	image  string
	tag    string
	logger *slog.Logger

	urlPropName string
	verbose     bool
	banner      bool

	// Runtime state, populated during Start and cleared by Stop.
	// container != nil is the canonical "currently running" check.
	container testcontainers.Container
	url       string
	logStop   context.CancelFunc // non-nil only while verbose log supervisor is running
	logDone   chan struct{}      // closed when the verbose log goroutine returns

	// Seams for unit testing. nil in production; set by internal tests to
	// avoid spinning up real containers or a real Docker daemon.
	containerRunFn    func(context.Context, string, ...testcontainers.ContainerCustomizer) (testcontainers.Container, error)
	newDockerClientFn func() (*dockerclient.Client, error)
}

// New creates a WireMock service with default configuration.
func New(name string) *WireMock {
	return &WireMock{
		name:        name,
		image:       defaultImage,
		tag:         defaultTag,
		logger:      slog.Default(),
		urlPropName: name + ".url",
	}
}

// WithImage sets the Docker image name.
func (t *WireMock) WithImage(image string) *WireMock { t.image = image; return t }

// WithTag sets the Docker image tag.
func (t *WireMock) WithTag(tag string) *WireMock { t.tag = tag; return t }

// WithURLPropertyName sets the property key under which the service URL is
// published. Default: "<name>.url".
func (t *WireMock) WithURLPropertyName(name string) *WireMock {
	t.urlPropName = name
	return t
}

// WithVerboseLogging enables WireMock's --verbose mode and streams the
// container's stdout/stderr into the testrig logger. Off by default to keep
// test output clean; turn on when you want to see every request the mock
// receives (matched stubs, unmatched requests, journal entries) in the same
// log stream as the rest of testrig.
func (t *WireMock) WithVerboseLogging() *WireMock { t.verbose = true; return t }

// WithBanner re-enables WireMock's ASCII art startup banner. The banner is
// suppressed by default to keep verbose output focused on request/match
// traffic; opt back in if you want the banner for visual confirmation of a
// fresh start. No effect without WithVerboseLogging — without a log consumer
// the banner is never delivered to the testrig logger regardless.
func (t *WireMock) WithBanner() *WireMock { t.banner = true; return t }

// argsForVerbose returns the CLI args to pass to the WireMock container.
// Returns nil when verbose is off so the image's default command is used
// unchanged. When verbose is on, --disable-banner is appended unless the
// caller opted into the startup banner via WithBanner.
func argsForVerbose(verbose, banner bool) []string {
	if !verbose {
		return nil
	}
	args := []string{"--verbose"}
	if !banner {
		args = append(args, "--disable-banner")
	}
	return args
}

// Name implements testrig.Service.
func (t *WireMock) Name() string { return t.name }

// Start implements testrig.Service. Returns an error if called while a
// previous Start is still active (i.e. Stop has not been called).
func (t *WireMock) Start(ctx context.Context, env testrig.EnvHandle) (testrig.Properties, error) {
	if t.container != nil {
		return nil, fmt.Errorf("wiremock service %q already started", t.name)
	}
	t.logger = env.Logger()
	t.logger.Info("🎬 Starting WireMock service", "name", t.name)

	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithExposedPorts(containerPort + "/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/__admin").
				WithPort(containerPort + "/tcp").
				WithStartupTimeout(60 * time.Second),
		),
	}
	if args := argsForVerbose(t.verbose, t.banner); args != nil {
		opts = append(opts, testcontainers.WithCmd(args...))
	}

	var (
		container testcontainers.Container
		err       error
	)
	if t.containerRunFn != nil {
		container, err = t.containerRunFn(ctx, fmt.Sprintf("%s:%s", t.image, t.tag), opts...)
	} else {
		container, err = testcontainers.Run(ctx, fmt.Sprintf("%s:%s", t.image, t.tag), opts...)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to start wiremock container: %w", err)
	}
	t.container = container

	if t.verbose {
		logCtx, logStop := context.WithCancel(context.Background())
		t.logStop = logStop
		t.logDone = make(chan struct{})
		go func() {
			defer close(t.logDone)
			t.streamLogs(logCtx, container.GetContainerID())
		}()
	}

	// Any failure past this point must release the container so the
	// instance stays reusable via a fresh Start.
	success := false
	defer func() {
		if success {
			return
		}
		t.haltLogStream()
		_ = container.Terminate(context.Background())
		t.container = nil
	}()

	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get wiremock host: %w", err)
	}
	port, err := container.MappedPort(ctx, containerPort+"/tcp")
	if err != nil {
		return nil, fmt.Errorf("failed to get wiremock mapped port: %w", err)
	}
	t.url = fmt.Sprintf("http://%s:%s", host, port.Port())

	success = true
	return testrig.Properties{
		t.urlPropName: t.url,
	}, nil
}

// Stop implements testrig.Service. Safe to call before Start or twice in
// a row. Releases the container and clears runtime state so the service can
// be Started again.
func (t *WireMock) Stop(ctx context.Context) error {
	if t.container == nil {
		return nil
	}
	t.logger.Info("🛑 Stopping WireMock service", "name", t.name)
	// Cancel the supervisor first so it doesn't try to restart after Terminate.
	// Terminate is what actually unblocks the in-flight ContainerLogs read,
	// so the wait for the goroutine to drain must come AFTER Terminate.
	if t.logStop != nil {
		t.logStop()
	}
	err := t.container.Terminate(ctx)
	t.waitLogStream()
	t.logStop = nil
	t.container = nil
	t.url = ""
	return err
}

// haltLogStream signals shutdown to the verbose log supervisor and waits for
// it to drain. Safe to call when verbose mode is off (no-op).
func (t *WireMock) haltLogStream() {
	if t.logStop != nil {
		t.logStop()
		t.logStop = nil
	}
	t.waitLogStream()
}

// waitLogStream blocks until the verbose log goroutine has returned, or
// logStopWait elapses. The bounded wait protects callers from a buggy or
// stuck Docker stream pinning Stop forever.
func (t *WireMock) waitLogStream() {
	if t.logDone == nil {
		return
	}
	select {
	case <-t.logDone:
	case <-time.After(logStopWait):
		t.logger.Warn("WireMock log stream did not stop within deadline", "name", t.name, "wait", logStopWait)
	}
	t.logDone = nil
}

// URL returns the WireMock service base URL. Only valid after Start.
func (t *WireMock) URL() string { return t.url }

// Client returns a WireMock client ready for fluent stubbing. Only valid after Start.
func (t *WireMock) Client() *wiremock.Client { return wiremock.NewClient(t.url) }

// logStreamOpener is the subset of the Docker client API used by streamLogs.
// The interface makes the streaming logic unit-testable without a live Docker daemon.
type logStreamOpener interface {
	ContainerLogs(ctx context.Context, containerID string, options dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error)
}

// lineWriter is an io.Writer that emits each Write call as a single slog entry.
// Trailing \r\n is trimmed; empty content after trimming is dropped.
type lineWriter struct {
	logger *slog.Logger
	level  slog.Level
}

func (w *lineWriter) Write(p []byte) (int, error) {
	// Docker frames are normally a single line, but stdcopy can deliver a
	// frame that contains embedded newlines (multi-line log entry, or several
	// short lines coalesced into one frame). Emit one slog entry per line so
	// they render correctly downstream.
	s := strings.TrimRight(string(p), "\r\n")
	if s == "" {
		return len(p), nil
	}
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		w.logger.Log(context.Background(), w.level, line)
	}
	return len(p), nil
}

// streamLogs opens a following multiplexed log stream for the given container
// and routes stdout (INFO) and stderr (WARN) into the testrig logger for the
// duration of ctx. It restarts on clean EOF — the symptom of Docker Desktop
// silently terminating idle ContainerLogs --follow HTTP streams on macOS. An
// error opening the stream or a non-EOF read error causes a clean exit without
// restart.
func (t *WireMock) streamLogs(ctx context.Context, containerID string) {
	newClientFn := func() (*dockerclient.Client, error) { return dockerclient.New(dockerclient.FromEnv) }
	if t.newDockerClientFn != nil {
		newClientFn = t.newDockerClientFn
	}
	dockerClient, err := newClientFn()
	if err != nil {
		t.logger.Warn("WireMock log stream: failed to create Docker client", "name", t.name, "err", err)
		return
	}
	defer func() { _ = dockerClient.Close() }()

	t.streamLogsFrom(ctx, containerID, dockerClient)
}

// streamLogsFrom is the testable core of streamLogs: it accepts a logStreamOpener
// so unit tests can inject a fake without a live Docker daemon.
func (t *WireMock) streamLogsFrom(ctx context.Context, containerID string, opener logStreamOpener) {
	outW := &lineWriter{logger: t.logger, level: slog.LevelInfo}
	errW := &lineWriter{logger: t.logger, level: slog.LevelWarn}

	// since bounds duplication on restart: on the second and onward iterations
	// we ask Docker only for entries newer than the moment the previous stream
	// returned EOF. Zero value (first iteration) means "from the beginning"
	// so we don't miss startup logs.
	var since string

	for {
		opts := dockerclient.ContainerLogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Follow:     true,
		}
		if since != "" {
			opts.Since = since
		}

		rc, err := opener.ContainerLogs(ctx, containerID, opts)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			t.logger.Warn("WireMock log stream failed to open", "name", t.name, "err", err)
			return
		}

		_, copyErr := stdcopy.StdCopy(outW, errW, rc)
		_ = rc.Close()

		if ctx.Err() != nil {
			return
		}
		if copyErr != nil {
			t.logger.Warn("WireMock log stream error", "name", t.name, "err", copyErr)
			return
		}

		// Clean EOF from Docker — restart. Record "now" so the next call
		// skips entries we've already delivered.
		since = time.Now().UTC().Format(time.RFC3339Nano)
		t.logger.Debug("WireMock log stream clean EOF, restarting", "name", t.name)

		// Backoff to avoid a tight loop if Docker keeps returning streams
		// that immediately close (e.g. container has gone away).
		select {
		case <-ctx.Done():
			return
		case <-time.After(logRestartBackoff):
		}
	}
}
