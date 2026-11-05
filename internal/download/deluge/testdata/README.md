# Deluge fixtures

```json
{"_fixture": "handwritten-from-docs"}
```

None of these files is a recorded response. `torrents_status.json` is the
answer shape of `core.get_torrents_status` through the Web JSON-RPC API
(`{"result", "error", "id"}`, https://deluge.readthedocs.io/en/latest/reference/webapi.html,
fetched 2026-11-05) with one torrent per state of `TORRENT_STATE`
(deluge/common.py), plus finished Paused/Queued torrents. Keys are the
documented status keys requested by the client; values are invented.
