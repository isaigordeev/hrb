// Package vault resolves vault paths, opens existing vaults, and
// initializes new ones on disk.
package vault

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/isdg/hr/internal/rc"
)

//go:embed default.toml
var defaultConfig []byte

type Vault struct {
	Root string
}

// Resolve returns the absolute root of the active vault for operating
// commands. Priority: explicit (flag) > $HR_VAULT > discovered from the
// current directory (walking up for hr.toml, like `.git`) > ~/.hrrc.
func Resolve(explicit string) (string, error) {
	raw := explicit
	if raw == "" {
		raw = os.Getenv("HR_VAULT")
	}
	if raw == "" {
		if cwd, err := os.Getwd(); err == nil {
			if root, ok := Discover(cwd); ok {
				raw = root
			}
		}
	}
	if raw == "" {
		r, err := rc.Load()
		if err != nil {
			return "", err
		}
		raw = r.Vault
	}
	if raw == "" {
		return "", fmt.Errorf(
			"no vault configured (run `hr init <name>`, " +
				"or cd into a cloned vault)")
	}
	return absExpand(raw)
}

// Discover walks up from dir looking for an hr.toml, the way `git`
// discovers a repo root from `.git`. It lets a cloned vault work as
// soon as you `cd` into it (or a subdirectory of it), with no ~/.hrrc
// or $HR_VAULT needed. Returns the vault root and whether one was found.
func Discover(dir string) (string, bool) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "hr.toml")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// ResolveNew returns the absolute path for a vault about to be
// created. Priority: explicit > $HR_VAULT > ~/blogs/<name>. Never
// reads ~/.hrrc.
func ResolveNew(explicit, name string) (string, error) {
	raw := explicit
	if raw == "" {
		raw = os.Getenv("HR_VAULT")
	}
	if raw == "" {
		raw = "~/blogs/" + name
	}
	return absExpand(raw)
}

func absExpand(p string) (string, error) {
	expanded, err := expandTilde(p)
	if err != nil {
		return "", err
	}
	return filepath.Abs(expanded)
}

func expandTilde(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}

func Open(root string) (*Vault, error) {
	v := &Vault{Root: root}
	if _, err := os.Stat(v.ConfigPath()); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"no hr vault at %s (run `hr init` first)", root)
		}
		return nil, err
	}
	return v, nil
}

func Init(root, name string) (*Vault, error) {
	v := &Vault{Root: root}

	if _, err := os.Stat(v.ConfigPath()); err == nil {
		return nil, fmt.Errorf(
			"vault already initialized at %s", root)
	}

	dirs := []string{v.Root, v.FeedsDir(), v.MetaDir(), v.LogDir()}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", d, err)
		}
	}

	err := os.WriteFile(v.ConfigPath(), buildConfig(name), 0o644)
	if err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	gitignore := filepath.Join(v.Root, ".gitignore")
	err = os.WriteFile(gitignore, []byte(".hr/\n"), 0o644)
	if err != nil {
		return nil, fmt.Errorf("write .gitignore: %w", err)
	}

	if err := rc.Save(&rc.RC{Vault: v.Root}); err != nil {
		return nil, fmt.Errorf("write hrrc: %w", err)
	}

	return v, nil
}

func buildConfig(name string) []byte {
	header := fmt.Sprintf("name = %q\n\n", name)
	return append([]byte(header), defaultConfig...)
}

func (v *Vault) ConfigPath() string {
	return filepath.Join(v.Root, "hr.toml")
}

func (v *Vault) FeedsDir() string {
	return filepath.Join(v.Root, "feeds")
}

// Rel renders p relative to the vault root with forward slashes, for
// use in messages. Paths outside the vault are returned unchanged.
func (v *Vault) Rel(p string) string {
	rel, err := filepath.Rel(v.Root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return filepath.ToSlash(rel)
}

func (v *Vault) MetaDir() string {
	return filepath.Join(v.Root, ".hr")
}

func (v *Vault) CachePath() string {
	return filepath.Join(v.MetaDir(), "cache.json")
}

// IndexPath is the listing cache: a single file mirroring every
// article's parsed frontmatter + meta so `hr list`/`feed` can skip
// re-parsing unchanged files. Rebuilt lazily; safe to delete.
func (v *Vault) IndexPath() string {
	return filepath.Join(v.MetaDir(), "index.json")
}

func (v *Vault) LogDir() string {
	return filepath.Join(v.MetaDir(), "log")
}

func (v *Vault) RawDir() string {
	return filepath.Join(v.MetaDir(), "raw")
}

// RawPath returns the archive location for an article's original HTML,
// mirroring the feeds/ layout: <vault>/.hr/raw/<feed>/<base>.html where
// <base> is the article filename minus its .md suffix.
func (v *Vault) RawPath(feedName, articleFilename string) string {
	base := strings.TrimSuffix(articleFilename, ".md")
	return filepath.Join(v.RawDir(), feedName, base+".html")
}
