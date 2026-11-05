// Package download defines the interface Gonema uses to drive download clients
// and the types shared by its implementations:
//
//   - sabnzbd and nzbget (Usenet), qbittorrent, deluge and transmission
//     (BitTorrent), one sub-package each, built from a Config;
//   - Item, the normalised view of a download, whose Status is one of the
//     downloads.status values of docs/data-model.md;
//   - PathMapper, the pure translation of remote_path_mappings;
//   - InfoHash and MagnetInfoHash, used as client references for torrents.
//
// # Errors
//
// Every client returns *Error values matching one sentinel: ErrUnreachable,
// ErrAuth, ErrURLBase, ErrTLS, ErrAPIDisabled, ErrUnexpected (not the expected
// kind of client), ErrRejected, ErrNotFound, ErrUnsupported, ErrInvalidRequest
// or ErrUnknownRef. Their text is plain language for the onboarding screen.
// When the caller's context ends, the error matches ctx.Err() instead.
// Credentials (API key, password, basic-auth token, session cookies and ids)
// are redacted from every error message, including transport errors that
// quote the request URL.
//
// # Transport
//
// Each HTTP request has its own timeout (Config.Timeout, default 30 s) and a
// bounded response body. Redirects are not followed. Certificates are verified
// unless Config.InsecureSkipVerify is set: an explicit opt-in for self-signed
// certificates on a trusted network, which exposes the credentials to a man in
// the middle.
//
// # Data safety
//
// Remove deletes data only when deleteData is true, which callers set only on
// an explicit user request. Clients never delete the final output of a
// completed Usenet download; the importer does it through internal/fsx.
package download
