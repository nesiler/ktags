// Package mask replaces secret material in free text before it reaches a log, the screen, an
// error or a run record (security.md §1, ADR-0002). Mask is a plain func(string) string so every
// boundary can take it as a value. Every pattern is matched against the original text and the
// matched ranges are merged before replacement, so one pattern's replacement never hides a
// secret from another. The package depends on the standard library only.
//
// Every masked range becomes the literal text "[masked]" (Placeholder); overlapping or touching
// ranges become one placeholder. For key/value pairs, flags and Bearer credentials only the value
// is replaced and the key stays readable: `password: hunter2` becomes `password: [masked]`.
//
// Known limits, all chosen to hide more rather than less:
//   - An unquoted value stops at white space; only the first word of a multi-word plain value is
//     masked unless it follows an anchor or tag.
//   - A value starting with '&' is treated as a YAML anchor, so the next word is masked as well,
//     even when it is a separate non-secret pair (`password=&x user=bob` masks `user=bob`).
//   - In the command-line form `--password &x word`, only `&x` is masked: a shell ends the command
//     at '&', so `word` is not the flag's value.
//   - Key names are matched anywhere in a word, so a non-secret key such as `private_key_file`
//     or `--private-key` (a path) is masked too.
//   - A key followed by a run of colons and no value (`token:::`) reads `token::[masked]`: the
//     last colon is taken as the value, because a value made of colons would otherwise leak.
package mask
