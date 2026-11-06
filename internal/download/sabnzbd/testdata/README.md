# SABnzbd fixtures

```json
{"_fixture": "handwritten-from-docs"}
```

None of these files is a recorded response. `queue.json` and `history.json`
start from the examples of the official API reference
(https://sabnzbd.org/wiki/configuration/5.1/api, fetched 2026-11-05), with names
changed to movies and slots added by hand to cover every mapped state:
`Queued`, `Propagating` and an `ENCRYPTED` label in the queue; two `Failed`
history entries ("Unpacking failed, write error or disk is full?", "Out of
retention") and one in post-processing (`Extracting`).
