package podman

import (
	"context"
	"os"
	"testing"
	"time"
)

// Integration test against a real Podman socket - skipped if one isn't
// configured or reachable, rather than failing the whole suite in
// environments without Podman set up.
func TestContainerLifecycle(t *testing.T) {
	socket := os.Getenv("PODMAN_SOCKET")
	if socket == "" {
		t.Skip("PODMAN_SOCKET not set")
	}

	client := NewClient(socket)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := client.Info(ctx); err != nil {
		t.Skipf("podman socket not reachable: %v", err)
	}

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
