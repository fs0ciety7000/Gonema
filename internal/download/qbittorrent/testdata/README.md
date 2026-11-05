# qBittorrent fixtures

```json
{"_fixture": "handwritten-from-docs"}
```

None of these files is a recorded response. `torrents.json` follows the field
list of `/api/v2/torrents/info` in the official WebUI API documentation
(https://github.com/qbittorrent/qBittorrent/wiki/WebUI-API-(qBittorrent-5.0),
fetched 2026-11-05) and has one torrent per state, covering the 4.x names
(`pausedUP`, `pausedDL`) and the 5.x names (`stoppedUP`, `stoppedDL`,
`forcedMetaDL`) taken from `src/webui/api/serialize/serialize_torrent.cpp`.
Hashes, sizes and paths are invented. `categories.json` is the documented
`/api/v2/torrents/categories` example with Gonema-like names.
