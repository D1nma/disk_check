package export

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/D1nma/disk_check/internal/scanner"
)

// Meta is the metadata object of an ncdu export.
type Meta struct {
	Progname  string // "disk-explorer" when we write
	Progver   string
	Timestamp int64 // unix seconds

	// disk-explorer extensions (optional, ignored by ncdu): the host that
	// was scanned and the usage of the filesystem holding the scan root, so
	// a remote scan can still show "% of disk".
	Host                     string
	FSTotal, FSUsed, FSAvail int64
}

// Format version written by WriteNcdu.
const (
	ncduMajor = 1
	ncduMinor = 2
)

// DefaultProgname is written by WriteNcdu when Meta.Progname is empty.
const DefaultProgname = "disk-explorer"

// WriteNcdu writes root as an ncdu JSON export (format [1,2,{meta},tree])
// readable by `ncdu -f`.
//
// root must be aggregated (as returned by scanner.Scan or ReadNcdu): a
// directory's own size is taken from OwnSize. An empty meta.Progname is
// written as DefaultProgname, an empty meta.Progver is omitted, and a zero
// meta.Timestamp is replaced by the current time. Entries are written in
// the order of Children. See the package documentation for what the format
// can and cannot represent.
//
// The output is streamed through a buffered writer; nothing proportional to
// the tree is built in memory besides a stack as deep as the tree.
func WriteNcdu(w io.Writer, root *scanner.Node, meta Meta) error {
	if root == nil {
		return errors.New("export ncdu : arbre vide")
	}
	if meta.Progname == "" {
		meta.Progname = DefaultProgname
	}
	if meta.Timestamp == 0 {
		meta.Timestamp = time.Now().Unix()
	}

	nw := ncduWriter{bw: bufio.NewWriterSize(w, 64<<10), line: make([]byte, 0, 512)}
	b := nw.line[:0]
	b = append(b, '[')
	b = strconv.AppendInt(b, ncduMajor, 10)
	b = append(b, ',')
	b = strconv.AppendInt(b, ncduMinor, 10)
	b = append(b, `,{"progname":`...)
	b = appendJSONString(b, meta.Progname)
	if meta.Progver != "" {
		b = append(b, `,"progver":`...)
		b = appendJSONString(b, meta.Progver)
	}
	b = append(b, `,"timestamp":`...)
	b = strconv.AppendInt(b, meta.Timestamp, 10)
	if meta.Host != "" {
		b = append(b, `,"de_host":`...)
		b = appendJSONString(b, meta.Host)
	}
	if meta.FSTotal > 0 {
		b = append(b, `,"de_fs_total":`...)
		b = strconv.AppendInt(b, meta.FSTotal, 10)
		b = append(b, `,"de_fs_used":`...)
		b = strconv.AppendInt(b, meta.FSUsed, 10)
		b = append(b, `,"de_fs_avail":`...)
		b = strconv.AppendInt(b, meta.FSAvail, 10)
	}
	b = append(b, "},\n"...)
	if root.IsDir {
		b = append(b, '[')
	}
	b = nw.appendInfo(b, root, true)
	if err := nw.write(b); err != nil {
		return err
	}

	if root.IsDir {
		if err := nw.writeChildren(root); err != nil {
			return err
		}
	}
	if err := nw.write(append(nw.line[:0], "]\n"...)); err != nil {
		return err
	}
	return nw.bw.Flush()
}

type ncduWriter struct {
	bw      *bufio.Writer
	line    []byte // reused buffer holding one entry
	lastDev uint64 // last synthetic device id given to a mount root
}

// write sends b to the buffered writer and keeps its storage for reuse.
func (nw *ncduWriter) write(b []byte) error {
	nw.line = b[:0]
	_, err := nw.bw.Write(b)
	return err
}

// writeChildren writes the entries below root (whose opening '[' and
// information object are already written) and closes every directory,
// root included. It is iterative so tree depth is bounded by memory only.
func (nw *ncduWriter) writeChildren(root *scanner.Node) error {
	type frame struct {
		n *scanner.Node
		i int // next child to write
	}
	stack := []frame{{n: root}}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.i == len(f.n.Children) {
			if err := nw.bw.WriteByte(']'); err != nil {
				return err
			}
			stack = stack[:len(stack)-1]
			continue
		}
		c := f.n.Children[f.i]
		f.i++
		if c == nil {
			continue
		}
		b := append(nw.line[:0], ",\n"...)
		if c.IsDir {
			b = append(b, '[')
		}
		b = nw.appendInfo(b, c, false)
		if err := nw.write(b); err != nil {
			return err
		}
		if c.IsDir {
			stack = append(stack, frame{n: c})
		}
	}
	return nil
}

// appendInfo appends n's information object.
func (nw *ncduWriter) appendInfo(b []byte, n *scanner.Node, isRoot bool) []byte {
	b = append(b, `{"name":`...)
	b = appendJSONString(b, n.Name)
	own := n.Size
	if n.IsDir {
		own = n.OwnSize
	}
	if own > 0 {
		b = append(b, `,"asize":`...)
		b = strconv.AppendInt(b, own, 10)
		b = append(b, `,"dsize":`...)
		b = strconv.AppendInt(b, own, 10)
	}
	if n.IsDir && !isRoot && n.Flags&scanner.FlagMountRoot != 0 {
		nw.lastDev++
		b = append(b, `,"dev":`...)
		b = strconv.AppendUint(b, nw.lastDev, 10)
	}
	if n.ModTime > 0 {
		b = append(b, `,"mtime":`...)
		b = strconv.AppendInt(b, n.ModTime, 10)
	}
	if n.Flags&scanner.FlagUnreadable != 0 {
		b = append(b, `,"read_error":true`...)
	}
	return append(b, '}')
}

const hexDigits = "0123456789abcdef"

// appendJSONString appends s as a JSON string literal. '"' and '\\' are
// escaped, \n \r \t use their short escapes, other control characters and
// DEL become \u00XX, and every byte that is not part of valid UTF-8 becomes
// U+FFFD (as encoding/json does), so the output is always valid UTF-8 JSON.
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0 // first byte of s not yet copied
	for i := 0; i < len(s); {
		c := s[i]
		if c >= 0x20 && c < 0x7f && c != '"' && c != '\\' {
			i++
			continue
		}
		if c >= 0x80 {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r != utf8.RuneError || size != 1 {
				i += size
				continue
			}
			dst = append(dst, s[start:i]...)
			dst = utf8.AppendRune(dst, utf8.RuneError)
			i++
			start = i
			continue
		}
		dst = append(dst, s[start:i]...)
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
		}
		i++
		start = i
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}
