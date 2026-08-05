package podman

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// TarFile needs no Podman, so these run everywhere.

func TestTarFileHeader(t *testing.T) {
	buffer, err := TarFile("run/job/backup.sh", 0o755, []byte("#!/bin/sh\necho hi\n"))
	if err != nil {
		t.Fatalf("TarFile: %v", err)
	}

	reader := tar.NewReader(buffer)
	header, err := reader.Next()
	if err != nil {
		t.Fatalf("reading tar header: %v", err)
	}

	if header.Name != "run/job/backup.sh" {
		t.Errorf("Name = %q", header.Name)
	}
	// 0755 is what lets argv be the script's own path, so the shebang picks
	// the interpreter and the platform stays language-agnostic.
	if header.Mode != 0o755 {
		t.Errorf("Mode = %o, want 755", header.Mode)
	}
	if header.Uid != 0 || header.Gid != 0 {
		t.Errorf("Uid/Gid = %d/%d, want 0/0 - ownership is stated, not inherited", header.Uid, header.Gid)
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("reading tar body: %v", err)
	}
	if string(body) != "#!/bin/sh\necho hi\n" {
		t.Errorf("body = %q", body)
	}

	if _, err := reader.Next(); err != io.EOF {
		t.Error("archive contains more than the one file")
	}
}

// TestTarFileRejectsEscapingPaths is the archive-shaped version of the rule
// task 1.11 proved for argv: a path arriving from a manifest is data, and must
// never be able to address something outside where it is unpacked. A tar entry
// named "../../etc/passwd" would otherwise be written there by the unpacker.
func TestTarFileRejectsEscapingPaths(t *testing.T) {
	for _, filePath := range []string{
		"",
		"/absolute/path.sh",
		"../escape.sh",
		"run/../../escape.sh",
		"./run/job.sh",
	} {
		if _, err := TarFile(filePath, 0o755, []byte("x")); err == nil {
			t.Errorf("TarFile(%q) accepted a path it must reject", filePath)
		}
	}
}

// TestPutArchiveDeliversAnExecutableScript is the whole of task 3.5's delivery
// mechanism, end to end against a real container: a script that exists only in
// memory is placed inside a container that was created but never started, and
// then runs - chosen by its shebang, with argv being nothing but its path.
func TestPutArchiveDeliversAnExecutableScript(t *testing.T) {
	client, ctx := newTestClient(t)

	const scriptPath = "run/job/hello.sh"
	id, err := client.CreateContainer(ctx, CreateContainerParams{
		RunID: 999998,
		Image: "docker.io/library/alpine:latest",
		// argv is the script's own path. Nothing here names an interpreter.
		Command: []string{"/" + scriptPath},
	})
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	t.Cleanup(func() {
		if err := client.RemoveContainer(context.Background(), id); err != nil {
			t.Logf("cleanup: RemoveContainer: %v", err)
		}
	})

	archive, err := TarFile(scriptPath, 0o755, []byte("#!/bin/sh\necho delivered-by-tar\n"))
	if err != nil {
		t.Fatalf("TarFile: %v", err)
	}

	// Between create and start, and into a container that has never run:
	// the filesystem exists from creation, and doing it after start would
	// race the entrypoint against the file it is meant to execute.
	if err := client.PutArchive(ctx, id, "/", archive); err != nil {
		t.Fatalf("PutArchive: %v", err)
	}

	if err := client.StartContainer(ctx, id); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}
	exitCode, err := client.WaitContainer(ctx, id)
	if err != nil {
		t.Fatalf("WaitContainer: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 - the delivered script did not run", exitCode)
	}

	var output strings.Builder
	if err := client.ReadContainerLogs(ctx, id, func(frame LogFrame) error {
		output.Write(frame.Data)
		return nil
	}); err != nil {
		t.Fatalf("ReadContainerLogs: %v", err)
	}
	if !strings.Contains(output.String(), "delivered-by-tar") {
		t.Errorf("container output = %q, want the delivered script's output", output.String())
	}
}

// TestPutArchiveCreatesMissingDirectories pins the property that removes the
// need for any host-side staging: /run/job does not exist in the image, and
// the unpacker creates it.
func TestPutArchiveCreatesMissingDirectories(t *testing.T) {
	client, ctx := newTestClient(t)

	id, err := client.CreateContainer(ctx, CreateContainerParams{
		RunID:   999997,
		Image:   "docker.io/library/alpine:latest",
		Command: []string{"ls", "/deeply/nested/created/by/tar/file.txt"},
	})
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	t.Cleanup(func() {
		if err := client.RemoveContainer(context.Background(), id); err != nil {
			t.Logf("cleanup: RemoveContainer: %v", err)
		}
	})

	archive, err := TarFile("deeply/nested/created/by/tar/file.txt", 0o644, []byte("x"))
	if err != nil {
		t.Fatalf("TarFile: %v", err)
	}
	if err := client.PutArchive(ctx, id, "/", archive); err != nil {
		t.Fatalf("PutArchive: %v", err)
	}
	if err := client.StartContainer(ctx, id); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}
	exitCode, err := client.WaitContainer(ctx, id)
	if err != nil {
		t.Fatalf("WaitContainer: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0 - the intermediate directories were not created", exitCode)
	}
}

func TestPutArchiveRejectsRelativeDestination(t *testing.T) {
	client, ctx := newTestClient(t)

	if err := client.PutArchive(ctx, "someid", "run/job", bytes.NewReader(nil)); err == nil {
		t.Error("PutArchive accepted a relative destination")
	}
}
