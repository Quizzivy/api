package wordconvert_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quizzivy/internal/platform/wordconvert"
)

func TestConverterRefusesMutableImageOrSharedWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, image := range []string{"debian:latest", "converter:development", "", "sha256:bad"} {
		if _, err := wordconvert.New(root, "/usr/bin/docker", image, time.Minute); !errors.Is(err, wordconvert.ErrConfiguration) {
			t.Fatalf("mutable runtime accepted: %v", err)
		}
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := wordconvert.New(root, "/usr/bin/docker", "sha256:"+strings.Repeat("a", 64), time.Minute); !errors.Is(err, wordconvert.ErrConfiguration) {
		t.Fatal("shared workspace accepted")
	}
}

func TestConverterRefusesInvalidInputBeforeInvokingRuntime(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("runtime deliberately rejects root workers")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	converter, err := wordconvert.New(root, filepath.Join(root, "nonexistent-docker"), "sha256:"+strings.Repeat("a", 64), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"doc", "docx", "pdf", "../docx"} {
		if _, err := converter.Convert(context.Background(), bytes.NewReader([]byte("not a word source")), format); !errors.Is(err, wordconvert.ErrSource) {
			t.Fatalf("invalid input reached runtime: %v", err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("invalid-input staging leaked")
	}
}

func TestConverterRefusesSymlinkWorkspace(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := wordconvert.New(link, "/usr/bin/docker", "sha256:"+strings.Repeat("a", 64), time.Minute); !errors.Is(err, wordconvert.ErrConfiguration) {
		t.Fatal("symlink workspace accepted")
	}
}

func TestConverterDiskWatchdogStopsAndCleansOversizedJob(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("runtime deliberately rejects root workers")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Join(t.TempDir(), "fake-docker")
	script := "#!" + python + ` 
import pathlib,sys,time
if sys.argv[1]=='run':
 mount=next(arg for arg in sys.argv if arg.startswith('type=bind,src=') and arg.endswith(',dst=/work'))
 work=pathlib.Path(mount[len('type=bind,src='):-len(',dst=/work')])
 with (work/'oversized').open('wb') as f: f.truncate(257*1024*1024)
 time.sleep(10)
`
	if err := os.WriteFile(runtime, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	converter, err := wordconvert.New(root, runtime, "sha256:"+strings.Repeat("a", 64), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	input := []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}
	if _, err := converter.Convert(context.Background(), bytes.NewReader(input), "doc"); !errors.Is(err, wordconvert.ErrLimit) {
		t.Fatalf("disk limit ignored: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("oversized job retained")
	}
}

// Docker wakes a waiting "docker run" before it records the exit in its
// container list, so a slot that has just exited can still be listed as
// running (Quizzivy/api#37). The fake runtime answers as Docker does in that
// window: the list filtered by status leaves the slot out, inspect says
// exited. The slot must be reclaimed, not reported busy.
func TestConverterReclaimsExitedSlotTheListStillCallsRunning(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("runtime deliberately rejects root workers")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	runtime := filepath.Join(t.TempDir(), "fake-docker")
	script := "#!" + python + `
import pathlib,sys
state=pathlib.Path(` + "'" + state + "'" + `)
removed=(state/'removed').exists()
args=sys.argv[1:]
with (state/'log').open('a') as log: log.write(args[0]+'\n')
if args[0]=='ps':
 if any(a.startswith('label=quizzivy.word-converter-owner=') for a in args) or any(a.startswith('status=') for a in args) or removed:
  sys.exit(0)
 print('0123456789ab')
elif args[0]=='inspect':
 print('exited')
elif args[0]=='rm':
 (state/'removed').touch()
elif args[0]=='run':
 sys.exit(1 if removed else 125)
`
	if err := os.WriteFile(runtime, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	converter, err := wordconvert.New(root, runtime, "sha256:"+strings.Repeat("a", 64), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	input := []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}
	_, err = converter.Convert(context.Background(), bytes.NewReader(input), "doc")
	if errors.Is(err, wordconvert.ErrBusy) {
		t.Fatal("an exited slot read as busy")
	}
	if !errors.Is(err, wordconvert.ErrConversion) {
		t.Fatalf("the fake run's failure was not reported: %v", err)
	}
	log, err := os.ReadFile(filepath.Join(state, "log"))
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Fields(string(log))
	rm, run := -1, -1
	for i, call := range calls {
		if call == "rm" && rm < 0 {
			rm = i
		}
		if call == "run" && run < 0 {
			run = i
		}
	}
	if rm < 0 || run < 0 || rm > run {
		t.Fatalf("the slot was not removed before the run: %v", calls)
	}
}
