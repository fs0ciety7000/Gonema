# NZBGet fixtures

```json
{"_fixture": "handwritten-from-docs"}
```

None of these files is a recorded response. They are written by hand from the
field lists of the official API reference (https://nzbget.com/documentation/api/,
methods `listgroups`, `history` and `config`, fetched 2026-11-05). Values are
invented; the first `listgroups` entry carries a size above 4 GiB to exercise
the Hi/Lo 64-bit fields. `history.json` covers SUCCESS, WARNING, FAILURE
(including a URL fetch failure) and a hidden DUP entry that must be skipped.
