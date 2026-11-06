// Package torznab implements indexer.Indexer with the Torznab API, a superset of
// Newznab for torrents (https://torznab.github.io/spec-1.3-draft/). The XML
// layer, error mapping and HTTP client are shared with internal/indexer/newznab;
// this package sets the BitTorrent protocol, under which the Torznab attributes
// (seeders, peers, infohash, magneturl, volume factors, tags) are read.
package torznab

import (
	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/indexer/newznab"
)

// Options configures a Torznab client; see newznab.Options. Info.Protocol is
// forced to BitTorrent.
type Options = newznab.Options

// New returns a Torznab client.
func New(o Options) (*newznab.Client, error) {
	o.Info.Protocol = decision.Torrent
	return newznab.New(o)
}
