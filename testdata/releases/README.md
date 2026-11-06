# Release name corpus

`corpus.jsonl` is the golden corpus of `internal/parser` (`TestCorpus`, `FuzzParse` seeds,
`BenchmarkParse`). One JSON object per line:

```json
{"name":"<release name>","source":"predb|known|synthetic","want":{...},"note":"optional"}
```

- `want` is a `parser.ParsedRelease` in its JSON form. Omitted fields are expected to be
  zero; an omitted `revision` means `{"version":1}`. Unknown fields fail the test.
- `note` explains an ambiguous or surprising expectation.

## Provenance

- `predb`: real scene names fetched on 2026-11-05 from the public JSON API of
  `api.predb.net` (65 search/section queries, one every 2.5 s, no authentication;
  predb.net `robots.txt` allows all). Movie sections only; TV, music and subpacks excluded.
  Kept a subset with distinct parse signatures so that every tag family is covered.
- `known`: real names recalled from P2P/scene naming knowledge. None at the moment:
  P2P names could not be verified against a public source, so they are `synthetic`.
- `synthetic`: constructed edge cases (P2P naming, cams, anime brackets, obfuscated
  Usenet names, paths, spacing). Group names are placeholders (`GROUP`). Must stay a
  minority (enforced by `TestCorpus`).

## Adding an entry

Write the expected values by hand, run `go test ./internal/parser/ -run TestCorpus`,
and fix the parser (not the expectation) unless the name is genuinely ambiguous, in
which case document the decision in `note`.
