package locking

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/github/git-lfs/tools"

	"github.com/github/git-lfs/config"
)

var (
	// lockable patterns from .gitattributes
	cachedLockablePatterns []string
	cachedLockableMutex    sync.Mutex
)

// GetLockablePatterns returns a list of patterns in .gitattributes which are
// marked as lockable
func GetLockablePatterns() []string {
	cachedLockableMutex.Lock()
	defer cachedLockableMutex.Unlock()

	// Only load once
	if cachedLockablePatterns == nil {
		// Always make non-nil even if empty
		cachedLockablePatterns = make([]string, 0, 10)

		paths := config.GetAttributePaths()
		for _, p := range paths {
			if p.Lockable {
				cachedLockablePatterns = append(cachedLockablePatterns, p.Path)
			}
		}
	}

	return cachedLockablePatterns

}

// RefreshLockablePatterns causes us to re-read the .gitattributes and caches the result
func RefreshLockablePatterns() {
	cachedLockableMutex.Lock()
	defer cachedLockableMutex.Unlock()
	cachedLockablePatterns = nil
}

// IsFileLockable returns whether a specific file path is marked as Lockable,
// ie has the 'lockable' attribute in .gitattributes
// Lockable patterns are cached once for performance, unless you call RefreshLockablePatterns
// path should be relative to repository root
func IsFileLockable(path string) bool {
	patterns := GetLockablePatterns()
	for _, wildcard := range patterns {
		// Convert wildcards to regex
		regStr := "^" + regexp.QuoteMeta(wildcard)
		regStr = strings.Replace(regStr, "\\*", ".*", -1)
		regStr = strings.Replace(regStr, "\\?", ".", -1)
		reg := regexp.MustCompile(regStr)

		if reg.MatchString(path) {
			return true
		}
	}
	return false
}

// FixAllLockableFileWriteFlags recursively scans the repo looking for files which
// are lockable, and makes sure their write flags are set correctly based on
// whether they are currently locked or unlocked.
// Files which are unlocked are made read-only, files which are locked are made
// writeable.
// This function can be used after a clone or checkout to ensure that file
// state correctly reflects the locking state
func FixAllLockableFileWriteFlags() error {
	return FixLockableFileWriteFlagsInDir("", true)
}

// FixLockableFileWriteFlagsInDir scans dir (which can either be a relative dir
// from the root of the repo, or an absolute dir within the repo) looking for
// files which are lockable, and makes sure their write flags are set correctly
// based on whether they are currently locked or unlocked. Files which are
// unlocked are made read-only, files which are locked are made writeable.
func FixLockableFileWriteFlagsInDir(dir string, recursive bool) error {
	absPath := dir
	if !filepath.IsAbs(dir) {
		absPath = filepath.Join(config.LocalWorkingDir, dir)
	}
	stat, err := os.Stat(absPath)
	if err != nil {
		return err
	}
	if !stat.IsDir() {
		return fmt.Errorf("%q is not a valid directory", dir)
	}

	// For simplicity, don't use goroutines to parallelise recursive scan
	// This routine is almost certainly disk-limited anyway
	// We don't need sorting so don't use ioutil.Readdir or filepath.Walk
	d, err := os.Open(absPath)
	if err != nil {
		return err
	}

	contents, err := d.Readdir(-1)
	if err != nil {
		return err
	}
	for _, fi := range contents {
		abschild := filepath.Join(absPath, fi.Name())
		if fi.IsDir() {
			if recursive {
				err = FixLockableFileWriteFlagsInDir(abschild, recursive)
			}
			continue
		}

		// This is a file, get relative to repo root
		relpath, err := filepath.Rel(config.LocalWorkingDir, abschild)
		if err != nil {
			return err
		}
		// Convert to git-style forward slash separators if necessary
		// Necessary to match attributes
		if filepath.Separator == '\\' {
			relpath = strings.Replace(relpath, "\\", "/", -1)
		}
		if IsFileLockable(relpath) {
			err = tools.SetFileWriteFlag(relpath, IsFileLockedByCurrentCommitter(relpath))
			if err != nil {
				return err
			}
		}

	}
	return nil
}
