// Package dockerlog provides Docker container log streaming for testcontainers-based
// services. The Supervisor type manages a background goroutine that forwards
// container stdout/stderr into a slog.Logger, with automatic restart on clean
// EOF (a common Docker Desktop behaviour on macOS).
package dockerlog

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	dockerclient "github.com/moby/moby/client"
)

// defaultMaxLineLength is the maximum length of a buffered log line before it
// is split and flushed. Individual lineWriter instances may override this via
// the maxLineLen field.
const defaultMaxLineLength = 64 * 1024

// opener is the subset of dockerclient.Client needed for log streaming.
// Kept unexported so callers depend on Supervisor, not on the Docker client API.
type opener interface {
	ContainerLogs(ctx context.Context, containerID string, options dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error)
}

// newDockerOpener creates a Docker client and wraps it as an opener. This is
// the default factory used when Supervisor.NewOpenerFn is nil. It is a
// top-level func (not a package-level var) so tests cannot mutate it; tests
// that need a fake opener inject via Supervisor.NewOpenerFn instead, which is
// safe under t.Parallel().
func newDockerOpener() (opener, func(), error) {
	c, err := dockerclient.New(dockerclient.FromEnv)
	if err != nil {
		return nil, nil, err
	}
	return c, func() {
		// Close returns the HTTP transport's idle-pool teardown error, which
		// is typically EOF on already-drained connections and not actionable.
		// Real Docker failures surface on the next ContainerLogs call, not here.
		_ = c.Close()
	}, nil
}

// Supervisor manages Docker container log streaming for a single container.
// The zero value is valid and not started. All public methods are safe to call
// concurrently.
//
// Exported fields (RestartBackoff, StopWait, NewOpenerFn) are configuration
// inputs: set them before Start and do not mutate them while the supervisor is
// running. The streaming goroutine reads each one without synchronization at
// most once per cycle, and Start snapshots the timing values into private state
// so later writes would race the goroutine without taking effect. Reconfigure
// only between Cancel+Wait and the next Start.
//
// Intended lifecycle (one cycle):
//
//	supervisor.Start(containerID, logger)   // after container starts
//	supervisor.Cancel()                     // before container.Terminate()
//	container.Terminate(ctx)                // unblocks the in-flight read
//	supervisor.Wait()                       // after container.Terminate()
//
// The instance is reusable: after a Cancel+Wait cycle, Start may be called again.
type Supervisor struct {
	// RestartBackoff is the pause between successive ContainerLogs calls after a
	// clean EOF. Prevents tight spin when Docker keeps closing idle follow streams
	// (common on Docker Desktop / macOS). Defaults to 500ms if zero at Start time.
	RestartBackoff time.Duration

	// StopWait is the maximum time Wait blocks for the goroutine to exit after
	// Cancel is called. Defaults to 5s if zero at Start time.
	StopWait time.Duration

	// NewOpenerFn overrides the opener factory used by stream. When nil,
	// newDockerOpener (a real Docker client) is used. The second return is a
	// cleanup func called on return (e.g. to close the client); nil means no
	// cleanup. Set in tests to inject a fake opener without a real daemon.
	NewOpenerFn func() (opener, func(), error)

	// mu guards logStop, logDone, logger, and effectiveStopWait.
	mu      sync.Mutex
	logger  *slog.Logger
	logStop context.CancelFunc
	logDone chan struct{}
	// effectiveStopWait is the StopWait value resolved at Start time (with
	// defaults applied). Stored separately so Wait honors the value Start
	// observed even if the caller later mutates the exported StopWait field.
	effectiveStopWait time.Duration
}

// Running reports whether the streaming goroutine is active.
func (s *Supervisor) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logDone == nil {
		return false
	}
	select {
	case <-s.logDone:
		return false
	default:
		return true
	}
}

// Start spawns the streaming goroutine for containerID. logger is used for all
// emitted log entries and supervisor warnings. No-op if already running.
func (s *Supervisor) Start(containerID string, logger *slog.Logger) {
	s.mu.Lock()
	if s.logDone != nil {
		select {
		case <-s.logDone:
			if s.logStop != nil {
				s.logStop()
			}
			s.logStop = nil
			s.logDone = nil
			s.logger = nil
			s.effectiveStopWait = 0
		default:
			s.mu.Unlock()
			return
		}
	}
	// Snapshot all tunables under the lock so the goroutine never reads
	// the exported fields directly. This prevents data races if a caller
	// violates the "set before Start" contract, and is consistent with the
	// treatment of RestartBackoff and StopWait. The exported fields are not
	// mutated — callers own them.
	backoff := s.RestartBackoff
	if backoff == 0 {
		backoff = 500 * time.Millisecond
	}
	stopWait := s.StopWait
	if stopWait == 0 {
		stopWait = 5 * time.Second
	}
	makeOpener := s.NewOpenerFn // snapshot so the goroutine never reads the field
	s.logger = logger
	s.effectiveStopWait = stopWait
	logCtx, logStop := context.WithCancel(context.Background())
	s.logStop = logStop
	s.logDone = make(chan struct{})
	logDone := s.logDone
	s.mu.Unlock()

	go func() {
		defer close(logDone)
		s.stream(logCtx, containerID, logger, backoff, makeOpener)
	}()
}

// Cancel cancels the streaming goroutine's context. Call BEFORE container.Terminate()
// so the supervisor is in a shutdown state when the blocking ContainerLogs read is
// unblocked by the container going away. No-op if not running.
func (s *Supervisor) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logStop != nil {
		s.logStop()
		s.logStop = nil
	}
}

// Wait blocks until the streaming goroutine exits or StopWait elapses. Call
// AFTER container.Terminate() because Terminate is what unblocks the blocking
// ContainerLogs read. Logs a warning on timeout. No-op if not running.
//
// On timeout, logDone is NOT cleared — the goroutine may still be alive, and
// allowing Start to spawn a second goroutine would leak. A watchdog clears
// logDone once the original goroutine eventually exits, making the Supervisor
// reusable again at that point.
func (s *Supervisor) Wait() {
	s.mu.Lock()
	logDone := s.logDone
	stopWait := s.effectiveStopWait
	logger := s.logger
	s.mu.Unlock()

	if logDone == nil {
		return
	}

	select {
	case <-logDone:
		s.clearCycleState(logDone)
	case <-time.After(stopWait):
		if logger != nil {
			// Surface the operational consequence: if the streaming goroutine
			// is wedged (rare, but possible in a stuck Docker syscall), it —
			// along with the watchdog this spawns below — will remain alive
			// until the process exits.
			logger.Warn(
				"log stream did not stop within deadline; goroutine may leak until process exit",
				"deadline", stopWait,
			)
		}
		go func() {
			<-logDone
			s.clearCycleState(logDone)
		}()
	}
}

// clearCycleState releases per-cycle state (logDone, logger, effectiveStopWait)
// once the streaming goroutine has actually exited. The logDone identity check
// guards against a Start/Cancel/Wait cycle racing with a previous cycle's
// watchdog: only the cycle whose channel matches s.logDone is permitted to
// clear, so a stale watchdog never wipes fields owned by a fresh cycle.
func (s *Supervisor) clearCycleState(logDone chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logDone != logDone {
		return
	}
	if s.logStop != nil {
		s.logStop()
		s.logStop = nil
	}
	s.logDone = nil
	s.logger = nil
	s.effectiveStopWait = 0
}

// stream resolves the opener factory and delegates to streamFrom. It is the
// entry point for the streaming goroutine started by Start. All parameters
// are passed by value — the goroutine never reads any Supervisor field after
// this point, so callers cannot race it by mutating exported fields.
func (s *Supervisor) stream(ctx context.Context, containerID string, logger *slog.Logger, backoff time.Duration, makeOpener func() (opener, func(), error)) {
	if makeOpener == nil {
		makeOpener = newDockerOpener
	}
	o, cleanup, err := makeOpener()
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		logger.Warn("failed to create Docker client for log streaming", "err", err)
		return
	}
	if cleanup != nil {
		defer cleanup()
	}
	s.streamFrom(ctx, containerID, o, logger, backoff)
}

// streamFrom runs the restart-on-EOF supervisor loop. It opens a ContainerLogs
// follow stream, routes stdout→INFO and stderr→WARN into the logger, and restarts
// after a clean EOF. The loop exits when ctx is cancelled or a non-EOF error occurs.
// logger and backoff are parameters (not field reads) to avoid racing with any
// concurrent mutation of the exported Supervisor fields.
func (s *Supervisor) streamFrom(ctx context.Context, containerID string, o opener, logger *slog.Logger, backoff time.Duration) {
	// since="" tells the Docker daemon to stream from the start of the
	// container's log. That is the correct initial value: a Supervisor is
	// started right after the container starts, so "everything so far" is
	// expected to be a small handful of lines. Initializing to time.Now()
	// instead would drop those early lines on slow opens. Subsequent
	// iterations overwrite `since` with the timestamp of the previous EOF.
	var since string
	outW := &lineWriter{ctx: ctx, logger: logger, level: slog.LevelInfo, maxLineLen: defaultMaxLineLength}
	errW := &lineWriter{ctx: ctx, logger: logger, level: slog.LevelWarn, maxLineLen: defaultMaxLineLength}
	defer func() {
		outW.Flush()
		errW.Flush()
	}()

	// currentBackoff grows exponentially on consecutive fast empty EOFs
	// (container stopped / Docker Desktop idle-stream close on macOS) and
	// resets when the stream delivers data or runs for a meaningful duration.
	currentBackoff := backoff
	const maxBackoff = 10 * time.Second

	for {
		start := time.Now()
		rc, err := o.ContainerLogs(ctx, containerID, dockerclient.ContainerLogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Follow:     true,
			Since:      since,
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Warn("log stream failed to open", "err", err)
			return
		}

		// Wrap the lineWriters with byte counters so we can detect fast empty
		// EOFs without allocating on the hot path (the counter is stack-allocated
		// and the indirection cost is negligible compared to log I/O).
		outCounter := &byteCounter{w: outW}
		errCounter := &byteCounter{w: errW}
		_, copyErr := stdcopy.StdCopy(outCounter, errCounter, rc)
		_ = rc.Close()

		if copyErr != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Warn("log stream error", "err", copyErr)
			return
		}

		// Exponential backoff: if the stream returned immediately with no data
		// (a sign that the container is stopped/exited or Docker closed an idle
		// follow stream), double the wait to avoid a tight polling spin.
		// Reset to the base backoff once a stream delivers data or runs long
		// enough that it looked like a healthy live stream.
		duration := time.Since(start)
		totalBytes := outCounter.count + errCounter.count
		if duration < 100*time.Millisecond && totalBytes == 0 {
			currentBackoff *= 2
			if currentBackoff > maxBackoff {
				currentBackoff = maxBackoff
			}
		} else {
			currentBackoff = backoff
		}

		// Clean EOF — Docker Desktop closes idle follow streams on macOS.
		// Record timestamp to bound duplicate entries on the next open.
		// Note: `since` is interpreted by the Docker daemon against its own
		// clock and is inclusive, so lines emitted in the same nanosecond as
		// this timestamp may appear twice. See the wall-clock-skew caveat in
		// README.md — both apply across each restart.
		since = time.Now().UTC().Format(time.RFC3339Nano)
		logger.Debug("log stream clean EOF, restarting", "since", since)

		select {
		case <-ctx.Done():
			return
		case <-time.After(currentBackoff):
		}
	}
}

// byteCounter is a thin io.Writer wrapper that counts the bytes written through
// it. Used in streamFrom to detect fast empty EOFs without allocating on the
// hot path — the struct is stack-allocated by the compiler in the common case.
type byteCounter struct {
	w     io.Writer
	count uint64
}

func (c *byteCounter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.count += uint64(n)
	return n, err
}

// lineWriter is an io.Writer that emits each non-empty line as a structured
// slog entry at the configured level. Writes are not guaranteed to align on
// line boundaries — Docker's multiplexed framing or any io.Copy buffer can
// split a single logical line across two Write calls — so a trailing fragment
// is retained in buf until a newline arrives. Flush emits any residue when
// the stream ends.
//
// ctx is the streaming goroutine's context, passed to slog.Log so handlers
// can extract request/trace IDs the caller attached via the env's logger.
type lineWriter struct {
	ctx        context.Context
	logger     *slog.Logger
	level      slog.Level
	maxLineLen int    // max buffered line length before forced flush; use defaultMaxLineLength
	buf        []byte // bytes received since the last newline; never contains '\n'
}

func (w *lineWriter) Write(p []byte) (int, error) {
	maxLineLen := w.maxLineLen
	if maxLineLen <= 0 {
		maxLineLen = defaultMaxLineLength
	}
	rest := p
	for {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			for len(w.buf)+len(rest) > maxLineLen {
				chunkSize := maxLineLen - len(w.buf)
				w.buf = append(w.buf, rest[:chunkSize]...)
				w.emit(w.buf)
				w.buf = w.buf[:0]
				rest = rest[chunkSize:]
			}
			w.buf = append(w.buf, rest...)
			return len(p), nil
		}

		if len(w.buf)+i > maxLineLen {
			chunkSize := maxLineLen - len(w.buf)
			w.buf = append(w.buf, rest[:chunkSize]...)
			w.emit(w.buf)
			w.buf = w.buf[:0]
			rest = rest[chunkSize:]
			continue
		}

		// Build the line into a fresh allocation so it never shares the
		// backing array of w.buf. append(w.buf, ...) can return a slice
		// that aliases w.buf when buf has spare capacity; resetting
		// w.buf[:0] afterwards would then allow future appends to
		// overwrite the bytes passed to emit.
		var line []byte
		if len(w.buf) == 0 {
			line = rest[:i]
		} else {
			line = make([]byte, len(w.buf)+i)
			copy(line, w.buf)
			copy(line[len(w.buf):], rest[:i])
			w.buf = w.buf[:0]
		}
		w.emit(line)
		rest = rest[i+1:]
	}
}

// Flush emits any buffered partial line. Call when the underlying stream has
// ended (EOF or error) to avoid dropping a final line without a terminator.
func (w *lineWriter) Flush() {
	if len(w.buf) == 0 {
		return
	}
	line := w.buf
	w.buf = nil
	w.emit(line)
}

// emit trims a trailing CR and logs the line if non-empty.
func (w *lineWriter) emit(line []byte) {
	line = bytes.TrimRight(line, "\r")
	if len(line) == 0 {
		return
	}
	w.logger.Log(w.ctx, w.level, string(line))
}
