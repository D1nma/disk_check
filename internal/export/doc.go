// Package export converts scanner trees to and from open formats.
//
// # ncdu JSON (WriteNcdu, ReadNcdu)
//
// The ncdu export format (major version 1, see ncdu's "jsonfmt" document) is
//
//	[1, 2, {"progname":…,"progver":…,"timestamp":N}, <root>]
//
// where a directory is a JSON array whose first element is its information
// object, followed by its entries (files are objects, subdirectories are
// arrays). WriteNcdu writes minor version 2; ReadNcdu accepts any minor
// version of major version 1.
//
// Mapping between scanner.Node and the format, and what does not survive a
// round trip:
//
//   - Sizes. disk-explorer only measures allocated bytes, so WriteNcdu writes
//     "asize" = "dsize" = the node's own allocated size (OwnSize for a
//     directory, Size for a file); zero sizes are omitted. ReadNcdu only uses
//     "dsize" and ignores "asize".
//   - Hard links. WriteNcdu writes sizes as disk-explorer attributed them
//     (one link carries the size, the other links have dsize 0) and never
//     writes "hlnkc", so ncdu shows the same totals as disk-explorer.
//     Consequently scanner.FlagHardlink is not preserved by a round trip.
//     When reading a real ncdu export, every file with "hlnkc":true (or
//     "nlink" > 1) gets FlagHardlink, and each (dev, ino) pair is counted
//     once: the first occurrence in document order keeps its dsize, later
//     ones get Size 0. An "hlnkc" entry without "ino" cannot be matched and
//     keeps its size.
//   - Mount roots. A directory with scanner.FlagMountRoot is written with a
//     synthetic "dev" (1, 2, … in document order; the scan root has none),
//     which ncdu reads as "different filesystem from the parent". ReadNcdu
//     sets FlagMountRoot on every directory whose effective "dev" (absent
//     means inherited from the parent) differs from its parent's. The scan
//     root itself is never flagged.
//   - Errors. scanner.FlagUnreadable is written as "read_error":true and
//     read back from it, for directories and files alike. FlagPartial is not
//     written: ReadNcdu recomputes it (a directory is partial when a child is
//     unreadable or partial), like the scanner's post-scan aggregation.
//   - Modification time. "mtime" is written only when positive (the format
//     defines it as a positive integer) and read when present.
//   - Excluded entries ("excluded" set, whatever its value) were not scanned
//     and are skipped entirely by ReadNcdu, including excluded directories
//     and their content. Non-regular files ("notreg") are kept as files.
//   - Names. Strings are written as valid UTF-8 JSON: '"', '\\', control
//     characters and DEL are escaped, and bytes that are not valid UTF-8 are
//     replaced by U+FFFD (a file name that is not UTF-8 is therefore not
//     reproduced byte for byte). On reading, names are what encoding/json
//     decodes: raw invalid UTF-8 bytes (which ncdu itself may write) become
//     U+FFFD, and "\u00XX" escapes become the code point U+00XX. ReadNcdu
//     rejects entry names that are empty, ".", "..", or contain '/' or NUL,
//     so path-based code can trust an imported tree.
//   - Root. The root must be a directory in ncdu's format. As an extension,
//     WriteNcdu writes a non-directory root as a file object, and ReadNcdu
//     accepts such a root; ncdu itself would reject that document.
//
// ReadNcdu is streaming: it walks the document with encoding/json's token
// API and decodes one information object at a time, so its memory use is
// proportional to the resulting tree, not to the size of the text. Sizes and
// counts are aggregated as each directory closes, exactly like the scanner's
// post-scan pass. Truncated input is always an error (ErrTruncated), never a
// partial tree.
//
// # Prometheus (WriteProm)
//
// WriteProm writes the text exposition format consumed by node_exporter's
// textfile collector: per-directory disk usage, file count and an
// "incomplete" flag for the top levels of the tree, plus the scan
// timestamp. node_exporter reads the collector directory at any time, so
// callers should write to a temporary file in the same directory and rename
// it into place.
package export
