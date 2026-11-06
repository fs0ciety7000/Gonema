package download

import (
	"cmp"
	"slices"
	"strings"
)

// PathMapping translates the paths a download client reports on Host (remote
// paths) into the paths Gonema sees (remote_path_mappings row).
type PathMapping struct {
	Host       string // as configured on the download client (download_clients.host)
	RemotePath string // prefix seen by the client, e.g. "/downloads/" or `D:\Downloads\`
	LocalPath  string // same directory seen by Gonema, e.g. "/mnt/nas/downloads/"
}

// PathMapper applies remote path mappings. It is pure and safe for concurrent
// use. For a given host the longest matching remote prefix wins; matching is
// on whole path components, either separator is accepted, and Windows paths
// (drive letter or UNC) compare case-insensitively.
type PathMapper struct {
	byHost map[string][]normMapping
}

type normMapping struct {
	remote  []string // components of the remote prefix
	windows bool
	unc     bool
	local   string // local prefix without trailing separator
	sep     byte   // separator of the local path
}

// NewPathMapper returns a mapper for mappings. Entries with an empty host,
// remote or local path are ignored.
func NewPathMapper(mappings []PathMapping) *PathMapper {
	m := &PathMapper{byHost: make(map[string][]normMapping)}
	for _, pm := range mappings {
		host := normHost(pm.Host)
		if host == "" || strings.TrimSpace(pm.RemotePath) == "" || strings.TrimSpace(pm.LocalPath) == "" {
			continue
		}
		comps, win, unc := splitPath(pm.RemotePath)
		local := strings.TrimSpace(pm.LocalPath)
		sep := byte('/')
		if isWindowsPath(local) {
			sep = '\\'
		}
		trimmed := strings.TrimRight(local, `/\`)
		switch {
		case trimmed == "": // "/"
			trimmed = string(sep)
		case sep == '\\' && len(trimmed) == 2: // "C:"
			trimmed += `\`
		}
		m.byHost[host] = append(m.byHost[host], normMapping{remote: comps, windows: win, unc: unc, local: trimmed, sep: sep})
	}
	for h, ms := range m.byHost {
		slices.SortStableFunc(ms, func(a, b normMapping) int { return cmp.Compare(len(b.remote), len(a.remote)) })
		m.byHost[h] = ms
	}
	return m
}

// Map translates remote, reported by the client on host. ok is false when no
// mapping applies (or the remainder contains ".."); the path is then returned
// unchanged.
func (m *PathMapper) Map(host, remote string) (local string, ok bool) {
	if m == nil || remote == "" {
		return remote, false
	}
	ms := m.byHost[normHost(host)]
	if len(ms) == 0 {
		return remote, false
	}
	comps, win, unc := splitPath(remote)
	for _, nm := range ms {
		if nm.windows != win || nm.unc != unc || len(nm.remote) > len(comps) {
			continue
		}
		if !prefixEqual(nm.remote, comps, win) {
			continue
		}
		rest := comps[len(nm.remote):]
		if slices.Contains(rest, "..") {
			return remote, false // never map a path that climbs out of the prefix
		}
		if len(rest) == 0 {
			return nm.local, true
		}
		out := nm.local
		if out[len(out)-1] != nm.sep {
			out += string(nm.sep)
		}
		return out + strings.Join(rest, string(nm.sep)), true
	}
	return remote, false
}

func prefixEqual(prefix, comps []string, fold bool) bool {
	for i, p := range prefix {
		if fold {
			if !strings.EqualFold(p, comps[i]) {
				return false
			}
		} else if p != comps[i] {
			return false
		}
	}
	return true
}

func normHost(h string) string {
	return strings.ToLower(strings.TrimSuffix(strings.Trim(strings.TrimSpace(h), "[]"), "."))
}

// isWindowsPath reports a drive path (C:\ or C:/) or a UNC path (\\server).
func isWindowsPath(p string) bool {
	if len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) {
		return true
	}
	return strings.HasPrefix(p, `\\`)
}

// splitPath splits p on both separators, dropping empty and "." components.
// The drive ("C:") is the first component of a Windows path.
func splitPath(p string) (comps []string, windows, unc bool) {
	p = strings.TrimSpace(p)
	windows = isWindowsPath(p)
	unc = windows && strings.HasPrefix(p, `\\`)
	for c := range strings.FieldsFuncSeq(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if c == "." {
			continue
		}
		comps = append(comps, c)
	}
	return comps, windows, unc
}
