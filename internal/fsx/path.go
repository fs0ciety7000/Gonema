package fsx

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// Within reports whether p is root or inside it, lexically (both are cleaned;
// symbolic links are not resolved: see Contains).
func Within(root, p string) bool {
	root, p = filepath.Clean(root), filepath.Clean(p)
	if p == root {
		return true
	}
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// checkRel validates a path meant to stay below a root: relative, no volume, no
// ".." component, no NUL byte.
func checkRel(rel string) (string, error) {
	switch {
	case rel == "" || strings.ContainsRune(rel, 0):
		return "", fmt.Errorf("%w: empty or invalid relative path %q", ErrOutsideRoot, rel)
	case filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`):
		return "", fmt.Errorf("%w: %q is absolute", ErrOutsideRoot, rel)
	}
	for part := range strings.FieldsFuncSeq(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", fmt.Errorf("%w: %q goes up with \"..\"", ErrOutsideRoot, rel)
		}
	}
	clean := filepath.Clean(rel)
	if clean == "." {
		return "", fmt.Errorf("%w: %q names the root itself", ErrOutsideRoot, rel)
	}
	return clean, nil
}

// SafeJoin joins root and rel and checks that the result stays inside root, both
// lexically (rel is relative, without "..") and on disk: the deepest existing
// ancestor of the result, with its symbolic links resolved, must be inside root
// (resolved too). The result need not exist. root must exist.
func (o *Ops) SafeJoin(root, rel string) (string, error) {
	clean, err := checkRel(rel)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("%w: root %q is not absolute", ErrOutsideRoot, root)
	}
	root = filepath.Clean(root)
	joined := filepath.Join(root, clean)
	if !Within(root, joined) || joined == root {
		return "", fmt.Errorf("%w: %q", ErrOutsideRoot, rel)
	}
	if err := o.checkResolved(root, joined); err != nil {
		return "", err
	}
	return joined, nil
}

// checkResolved verifies that the deepest existing ancestor of p (p included)
// resolves inside root once symbolic links are followed.
func (o *Ops) checkResolved(root, p string) error {
	realRoot, err := o.fs.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("fsx: resolve %s: %w", root, err)
	}
	cur := p
	for {
		if _, err := o.fs.Lstat(cur); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return fmt.Errorf("fsx: no existing ancestor of %s", p)
		}
		cur = parent
	}
	resolvedCur, err := o.fs.EvalSymlinks(cur)
	if err != nil {
		return fmt.Errorf("fsx: resolve %s: %w", cur, err)
	}
	if !Within(realRoot, resolvedCur) {
		return fmt.Errorf("%w: %s resolves to %s, outside %s", ErrOutsideRoot, cur, resolvedCur, realRoot)
	}
	return nil
}

// Contains resolves the symbolic links of p (which must exist) and returns the
// first root (in order) that holds it, compared after resolving the roots too.
// Roots that do not exist are skipped. It returns ErrOutsideRoot when none does.
func (o *Ops) Contains(roots []string, p string) (resolved, root string, err error) {
	if !filepath.IsAbs(p) || strings.ContainsRune(p, 0) {
		return "", "", fmt.Errorf("%w: %q is not an absolute path", ErrOutsideRoot, p)
	}
	resolved, err = o.fs.EvalSymlinks(filepath.Clean(p))
	if err != nil {
		return "", "", err
	}
	for _, r := range roots {
		if r == "" || !filepath.IsAbs(r) {
			continue
		}
		rr, err := o.fs.EvalSymlinks(filepath.Clean(r))
		if err != nil {
			continue
		}
		if Within(rr, resolved) {
			return resolved, r, nil
		}
	}
	return "", "", fmt.Errorf("%w: %s", ErrOutsideRoot, p)
}
