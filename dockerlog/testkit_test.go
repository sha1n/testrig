package dockerlog

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	dockerclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTestkit_SupervisorController_Lifecycle(t *testing.T) {
	s := &Supervisor{}
	ctrl := SetupTestSpy(s)

	require.NotNil(t, ctrl, "expected SetupTestSpy to return a non-nil controller")
	require.NotNil(t, s.NewOpenerFn, "expected Supervisor.NewOpenerFn to be set")

	// Verify initial state
	assert.False(t, ctrl.Started(), "expected started to be false initially")
	assert.False(t, ctrl.Cancelled(), "expected cancelled to be false initially")
	assert.Empty(t, ctrl.ContainerID(), "expected empty container ID initially")

	// Call ContainerLogs
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result, err := ctrl.ContainerLogs(ctx, "test-cid", dockerclient.ContainerLogsOptions{})
	require.NoError(t, err)
	require.NotNil(t, result)
	defer func() { _ = result.Close() }()

	assert.True(t, ctrl.Started(), "expected started to be true after ContainerLogs call")
	assert.Equal(t, "test-cid", ctrl.ContainerID(), "expected container ID 'test-cid'")

	// Test WriteStream / WriteStdout / WriteStderr
	testPayload := "hello logs"
	go func() {
		_ = ctrl.WriteStdout(testPayload)
	}()

	// Verify bytes read from result match multiplexed frame format
	// Frame header is 8 bytes.
	buf := make([]byte, 8+len(testPayload))
	_, err = io.ReadFull(result, buf)
	require.NoError(t, err)

	assert.Equal(t, byte(1), buf[0], "expected stream type 1 (stdout)")
	payloadRead := string(buf[8:])
	assert.Equal(t, testPayload, payloadRead, "expected payload to match")

	// Write Stderr
	go func() {
		_ = ctrl.WriteStderr("error logs")
	}()

	buf2 := make([]byte, 8+len("error logs"))
	_, err = io.ReadFull(result, buf2)
	require.NoError(t, err)

	assert.Equal(t, byte(2), buf2[0], "expected stream type 2 (stderr)")
	assert.Equal(t, "error logs", string(buf2[8:]), "expected stderr payload to match")

	// Verify cancellation triggers closed channel
	select {
	case <-ctrl.Closed():
		t.Fatal("expected Closed() channel not to be closed yet")
	default:
	}

	cancel()

	// Wait for goroutine to process cancellation
	select {
	case <-ctrl.Closed():
		// Pass
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Closed() channel to close")
	}

	assert.True(t, ctrl.Cancelled(), "expected cancelled to be true after context cancellation")

	// Test writing to closed stream returns error
	err = ctrl.WriteStdout("more logs")
	assert.Error(t, err, "expected error writing to closed stream")
}

func TestTestkit_CloseController(t *testing.T) {
	s := &Supervisor{}
	ctrl := SetupTestSpy(s)

	// Close before started (should be no-op/no error)
	err := ctrl.Close()
	assert.NoError(t, err, "unexpected error on Close before Start")

	// Start and close
	ctx := context.Background()
	result, err := ctrl.ContainerLogs(ctx, "cid", dockerclient.ContainerLogsOptions{})
	require.NoError(t, err)

	err = ctrl.Close()
	assert.NoError(t, err, "unexpected error on Close")

	// Read should get EOF now
	buf := make([]byte, 10)
	_, err = result.Read(buf)
	assert.Equal(t, io.EOF, err, "expected EOF")
}

func TestTestkit_WriteBeforeStart_ReturnsError(t *testing.T) {
	s := &Supervisor{}
	ctrl := SetupTestSpy(s)
	err := ctrl.WriteStdout("hello")
	assert.Error(t, err, "expected error writing before stream is opened")
}

func TestTestkit_SetupTestSpy_SupervisorRun(t *testing.T) {
	s := &Supervisor{}
	ctrl := SetupTestSpy(s)

	logger := slog.Default()
	s.Start("cid", logger)
	s.Cancel()
	s.Wait()

	assert.True(t, ctrl.Started(), "expected supervisor to start and call opener")
}
