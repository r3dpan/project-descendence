package podman

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// newTestClient returns a client for the real Podman socket, or skips the
// test cleanly if PODMAN_SOCKET isn't set or isn't reachable - these are
// integration tests, not meant to fail the whole suite in an environment
// without Podman configured.
func newTestClient(t *testing.T) (*Client, context.Context) {
	t.Helper()

	socket := os.Getenv("PODMAN_SOCKET")
	if socket == "" {
		t.Skip("PODMAN_SOCKET not set")
	}

	client := NewClient(socket)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	if _, err := client.Info(ctx); err != nil {
		t.Skipf("podman socket not reachable: %v", err)
	}

	return client, ctx
}

func TestContainerLifecycle(t *testing.T) {
	client, ctx := newTestClient(t)

	id, err := client.CreateContainer(ctx, CreateContainerParams{
		RunID:   999999,
		Image:   "docker.io/library/alpine:latest",
		Command: []string{"sh", "-c", "exit 42"},
	})
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	t.Cleanup(func() {
		if err := client.RemoveContainer(context.Background(), id); err != nil {
			t.Logf("cleanup: RemoveContainer: %v", err)
		}
	})

	if err := client.StartContainer(ctx, id); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	exitCode, err := client.WaitContainer(ctx, id)
	if err != nil {
		t.Fatalf("WaitContainer: %v", err)
	}
	if exitCode != 42 {
		t.Errorf("exit code = %d, want 42", exitCode)
	}
}

// Task 1.11: argv must be built as a []string and passed straight through as
// the container's argv - never joined into a shell string. A single argv
// element containing shell metacharacters proves this either way: if it were
// shell-interpreted, "; rm -rf /" would run as its own command; passed
// through literally, the OCI runtime instead tries (and fails) to exec a
// file literally named "; rm -rf /", since no such file exists.
func TestCreateContainerArgvNeverShellInterpreted(t *testing.T) {
	client, ctx := newTestClient(t)

	const injectionAttempt = "; rm -rf /"

	id, err := client.CreateContainer(ctx, CreateContainerParams{
		RunID:   999998,
		Image:   "docker.io/library/alpine:latest",
		Command: []string{injectionAttempt},
	})
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	t.Cleanup(func() {
		if err := client.RemoveContainer(context.Background(), id); err != nil {
			t.Logf("cleanup: RemoveContainer: %v", err)
		}
	})

	err = client.StartContainer(ctx, id)
	if err == nil {
		t.Fatal("StartContainer succeeded, want an error - argv would have been shell-interpreted")
	}

	// The OCI runtime's error names the exact literal string it tried (and
	// failed) to find as a single file - proof the whole argument was
	// treated as one atomic token, not split on ';' into two commands.
	if !strings.Contains(err.Error(), injectionAttempt) {
		t.Errorf("error = %q, want it to reference the literal argument %q", err, injectionAttempt)
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Errorf("error = %q, want an exec-not-found error for %q, not evidence it ran as a shell command", err, injectionAttempt)
	}
}
