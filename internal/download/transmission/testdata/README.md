# Transmission fixtures

```json
{"_fixture": "handwritten-from-docs"}
```

Not a recorded response. `torrent_get.json` follows the `torrent-get`
response format and field list of the official RPC specification
(docs/rpc-spec.md of https://github.com/transmission/transmission, tag 4.0.6,
fetched 2026-11-05): one torrent per `status` value 0 to 6 (stopped both
unfinished and finished), the three `error` types (tracker warning, tracker
error, local error), a torrent labelled for another category and one without
label stored in the category directory. Values are invented.
