package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hr.toml"), []byte("name = \"t\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "feeds", "demo")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	got, ok := Discover(sub)
	if !ok || got != root {
		t.Fatalf("Discover(sub) = %q, %v; want %q, true", got, ok, root)
	}

	got, ok = Discover(root)
	if !ok || got != root {
		t.Fatalf("Discover(root) = %q, %v; want %q, true", got, ok, root)
	}

	elsewhere := t.TempDir()
	if _, ok := Discover(elsewhere); ok {
		t.Fatal("Discover(elsewhere) should find nothing")
	}
}

func TestResolvePrefersDiscoveryOverRC(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hr.toml"), []byte("name = \"t\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir()) // empty ~/.hrrc
	t.Setenv("HR_VAULT", "")

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	got, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	// macOS TempDir roots are under a /var symlink to /private/var;
	// os.Getwd() resolves it after Chdir, so compare resolved paths.
	wantReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != wantReal {
		t.Fatalf("Resolve() = %q, want %q", got, wantReal)
	}
}
