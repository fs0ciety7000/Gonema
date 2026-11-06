# Torznab fixtures

```json
{"_fixture": "handwritten-from-docs"}
```

No tracker account was used: **none of these files is a recorded response**.
They are written by hand from the Torznab specification 1.3 draft
(https://torznab.github.io/spec-1.3-draft/torznab/Specification-v1.3.html and
revisions 1.1 to 1.3, fetched on 2026-11-05).

| File | Source |
|---|---|
| `caps.xml` | caps example of the spec, `movie-search` with `tmdbid` (as listed by common Torznab proxies), a site-specific category (100001) and the `tags` element of revision 1.3 |
| `movie_search.xml` | attributes of the predefined table: seeders, peers, infohash (upper case, must be lowered), volume factors (revision 1.2, freeleech and a rejected negative value), minimum ratio and seed time, tags (revision 1.3, duplicated with another case); a magnet-only item with a base32 hash; an item without any download URL that must be skipped. The non-standard `<size>`, `<grabs>`, `<type>` elements emitted by common proxies are included. |
