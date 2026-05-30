package dockerlog

import (
	"context"
	"encoding/binary"
	"io"
	"sync"

	dockerclient "github.com/moby/moby/client"
)

// SupervisorController allows unit testing log streaming by controlling
// a mocked Supervisor's Docker stream.
type SupervisorController struct {
	mu          sync.Mutex
	containerID string
	started     bool
	cancelled   bool
	pw          *io.PipeWriter
	closed      chan struct{}
}

// SetupTestSpy configures a Supervisor to use a mocked opener controlled by the returned controller.
func SetupTestSpy(s *Supervisor) *SupervisorController {
	ctrl := &SupervisorController{
		closed: make(chan struct{}),
	}
	s.NewOpenerFn = func() (opener, func(), error) {
		return ctrl, func() {}, nil
	}
	return ctrl
}

// ContainerLogs implements the unexported opener interface.
func (c *SupervisorController) ContainerLogs(ctx context.Context, containerID string, options dockerclient.ContainerLogsOptions) (dockerclient.ContainerLogsResult, error) {
	c.mu.Lock()
	c.containerID = containerID
	c.started = true
	pr, pw := io.Pipe()
	c.pw = pw
	c.mu.Unlock()

	go func() {
		<-ctx.Done()
		c.mu.Lock()
		c.cancelled = true
		c.mu.Unlock()
		_ = pr.Close()
		_ = pw.Close()
		close(c.closed)
	}()

	return pr, nil
}

// WriteStream writes multiplexed data to the mock container log stream.
func (c *SupervisorController) WriteStream(streamType byte, payload string) error {
	c.mu.Lock()
	pw := c.pw
	c.mu.Unlock()
	if pw == nil {
		return io.ErrClosedPipe
	}

	header := make([]byte, 8)
	header[0] = streamType
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	if _, err := pw.Write(header); err != nil {
		return err
	}
	_, err := pw.Write([]byte(payload))
	return err
}

// WriteStdout writes stdout logs to the stream.
func (c *SupervisorController) WriteStdout(payload string) error {
	return c.WriteStream(1, payload)
}

// WriteStderr writes stderr logs to the stream.
func (c *SupervisorController) WriteStderr(payload string) error {
	return c.WriteStream(2, payload)
}

// Close closes the stream writer.
func (c *SupervisorController) Close() error {
	c.mu.Lock()
	pw := c.pw
	c.mu.Unlock()
	if pw != nil {
		return pw.Close()
	}
	return nil
}

// ContainerID returns the container ID the supervisor was started with.
func (c *SupervisorController) ContainerID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.containerID
}

// Started returns whether the log stream was opened.
func (c *SupervisorController) Started() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.started
}

// Cancelled returns whether the log stream context was cancelled.
func (c *SupervisorController) Cancelled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancelled
}

// Closed returns a channel that is closed when the stream context is cancelled.
func (c *SupervisorController) Closed() <-chan struct{} {
	return c.closed
}
