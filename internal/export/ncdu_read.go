package export

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/D1nma/disk_check/internal/scanner"
)

// Errors wrapped by ReadNcdu; test with errors.Is.
var (
	// ErrTruncated: the input ended before the document was complete
	// (including empty input), e.g. an interrupted stream.
	ErrTruncated = errors.New("export ncdu tronqué")
	// ErrUnsupportedVersion: the major version is not 1.
	ErrUnsupportedVersion = errors.New("version de format ncdu non prise en charge")
	// ErrFormat: the input is not valid JSON or not a valid ncdu export.
	ErrFormat = errors.New("export ncdu invalide")
)

// ncduBinaryMagic starts ncdu 2's binary export format (ncdu -O).
const ncduBinaryMagic = "\xbfncduEX1"

// noDev marks an absent "dev" field; a real device id never has this value.
const noDev = math.MaxUint64

// ReadNcdu parses an ncdu JSON export (major version 1, any minor) into a
// Node tree aggregated exactly like scanner.Scan's post-pass (Size, OwnSize,
// FileCount, DirCount, Parent, FlagPartial).
//
// The document is streamed: memory use is proportional to the tree, not to
// the input. The whole input must be one complete export followed only by
// whitespace; anything else is an error and no tree is returned. Errors wrap
// ErrTruncated, ErrUnsupportedVersion, ErrFormat, or the reader's own error.
// See the package documentation for how fields map to Node.
func ReadNcdu(r io.Reader) (*scanner.Node, Meta, error) {
	return readNcdu(r, false)
}

// readNcdu implements ReadNcdu. tokensOnly disables the fast path (see
// readEntry) so tests can check that both paths give the same result.
func readNcdu(r io.Reader, tokensOnly bool) (*scanner.Node, Meta, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	// Peek errors are ignored: the decoder reports them on its first read.
	head, _ := br.Peek(len(ncduBinaryMagic))
	if string(head) == ncduBinaryMagic {
		return nil, Meta{}, fmt.Errorf("import ncdu : %w : format binaire de ncdu 2, exporter en JSON (ncdu -o)", ErrFormat)
	}
	if strings.HasPrefix(string(head), "\xef\xbb\xbf") { // UTF-8 byte order mark
		_, _ = br.Discard(3)
	}
	p := &ncduReader{dec: json.NewDecoder(br), seen: make(map[inodeKey]struct{}), tokensOnly: tokensOnly}
	p.dec.UseNumber()
	root, meta, err := p.read()
	if err != nil {
		return nil, Meta{}, err
	}
	return root, meta, nil
}

type inodeKey struct {
	dev uint64
	ino uint64
}

// rawInfo receives one information object. Fields not listed are ignored.
type rawInfo struct {
	Name      string `json:"name"`
	Dsize     int64  `json:"dsize"`
	Mtime     int64  `json:"mtime"`
	Dev       uint64 `json:"dev"` // noDev when absent
	Ino       uint64 `json:"ino"` // 0 when absent
	Nlink     uint64 `json:"nlink"`
	Hlnkc     bool   `json:"hlnkc"`
	ReadError bool   `json:"read_error"`
	Excluded  any    `json:"excluded"`
}

// dirFrame is an open directory while parsing.
type dirFrame struct {
	node  *scanner.Node
	dev   uint64 // effective device id, inherited by entries without "dev"
	start int    // index in ncduReader.pending of the first child
}

type ncduReader struct {
	dec   *json.Decoder
	raw   rawInfo
	nodes nodeArena
	seen  map[inodeKey]struct{} // hard-linked inodes already counted
	// pending holds the children of every open directory, innermost last;
	// a directory's slice is copied out at exact size when it closes.
	pending    []*scanner.Node
	peek       [64]byte
	tokensOnly bool // tests: never peek, always take the token path
}

func (p *ncduReader) read() (*scanner.Node, Meta, error) {
	var meta Meta
	t, err := p.dec.Token()
	if err == io.EOF {
		return nil, meta, fmt.Errorf("import ncdu : %w : entrée vide", ErrTruncated)
	}
	if err != nil {
		return nil, meta, p.fail(err)
	}
	if t != json.Delim('[') {
		return nil, meta, p.formatErr("un export ncdu est un tableau JSON")
	}

	major, err := p.integer("version majeure")
	if err != nil {
		return nil, meta, err
	}
	if major != ncduMajor {
		return nil, meta, fmt.Errorf("import ncdu : %w : version majeure %d (seule la version %d est lue)",
			ErrUnsupportedVersion, major, ncduMajor)
	}
	if _, err := p.integer("version mineure"); err != nil {
		return nil, meta, err
	}

	if err := p.need("objet de métadonnées"); err != nil {
		return nil, meta, err
	}
	var rm struct {
		Progname  string `json:"progname"`
		Progver   string `json:"progver"`
		Timestamp any    `json:"timestamp"`
	}
	// The metadata is informative: a field of an unexpected type is ignored.
	if err := p.dec.Decode(&rm); err != nil {
		var te *json.UnmarshalTypeError
		if !errors.As(err, &te) {
			return nil, meta, p.fail(err)
		}
	}
	meta.Progname, meta.Progver = rm.Progname, rm.Progver
	if n, ok := rm.Timestamp.(json.Number); ok {
		meta.Timestamp, _ = numberToInt(n)
	}

	if err := p.need("répertoire racine"); err != nil {
		return nil, meta, err
	}
	root, err := p.readTree()
	if err != nil {
		return nil, meta, err
	}

	// Elements after the root are not defined by the format: skip them.
	for p.dec.More() {
		if err := p.skipValue(); err != nil {
			return nil, meta, err
		}
	}
	if err := p.expectDelim(']'); err != nil {
		return nil, meta, err
	}
	switch _, err := p.dec.Token(); {
	case err == io.EOF:
		return root, meta, nil
	case err != nil:
		return nil, meta, p.fail(err)
	default:
		return nil, meta, p.formatErr("données après la fin de l'export")
	}
}

// readTree reads the root entry and, for a directory, everything below it.
func (p *ncduReader) readTree() (*scanner.Node, error) {
	isDir, err := p.readEntry()
	if err != nil {
		return nil, err
	}
	if err := p.checkInfo(true); err != nil {
		return nil, err
	}
	root := &scanner.Node{Name: p.raw.Name, ModTime: p.raw.Mtime, IsDir: isDir}
	if p.raw.ReadError {
		root.Flags |= scanner.FlagUnreadable
	}
	if !isDir {
		root.Size = p.raw.Dsize
		root.FileCount = 1
		return root, nil
	}
	root.OwnSize = p.raw.Dsize
	rootDev := uint64(0)
	if p.raw.Dev != noDev {
		rootDev = p.raw.Dev
	}

	stack := []dirFrame{{node: root, dev: rootDev}}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		if !p.dec.More() {
			if err := p.expectDelim(']'); err != nil {
				return nil, err
			}
			p.closeDir(top)
			stack = stack[:len(stack)-1]
			continue
		}

		isDir, err := p.readEntry()
		if err != nil {
			return nil, err
		}
		if err := p.checkInfo(false); err != nil {
			return nil, err
		}
		if truthy(p.raw.Excluded) {
			// Not scanned by ncdu: drop it, with its content if any.
			if isDir {
				if err := p.skipRest(); err != nil {
					return nil, err
				}
			}
			continue
		}

		dev := top.dev
		if p.raw.Dev != noDev {
			dev = p.raw.Dev
		}
		n := p.nodes.new()
		*n = scanner.Node{Name: p.raw.Name, ModTime: p.raw.Mtime, Parent: top.node, IsDir: isDir}
		if p.raw.ReadError {
			n.Flags |= scanner.FlagUnreadable
		}
		p.pending = append(p.pending, n)
		if isDir {
			// Size is set when the directory closes.
			n.OwnSize = p.raw.Dsize
			if dev != top.dev {
				n.Flags |= scanner.FlagMountRoot
			}
			stack = append(stack, dirFrame{node: n, dev: dev, start: len(p.pending)})
			continue
		}
		n.Size = p.raw.Dsize
		n.FileCount = 1
		if p.raw.Hlnkc || p.raw.Nlink > 1 {
			n.Flags |= scanner.FlagHardlink
			if p.raw.Ino != 0 {
				k := inodeKey{dev, p.raw.Ino}
				if _, dup := p.seen[k]; dup {
					n.Size = 0
				} else {
					p.seen[k] = struct{}{}
				}
			}
		}
	}
	return root, nil
}

// closeDir gives f's directory its children (copied at exact size out of
// the shared pending stack) and aggregates it. Its subdirectories are
// already closed, hence final.
func (p *ncduReader) closeDir(f dirFrame) {
	if kids := p.pending[f.start:]; len(kids) > 0 {
		f.node.Children = make([]*scanner.Node, len(kids))
		copy(f.node.Children, kids)
		clear(kids)
		p.pending = p.pending[:f.start]
	}
	aggregateDir(f.node)
}

// checkInfo validates the information object just read into p.raw.
func (p *ncduReader) checkInfo(isRoot bool) error {
	name := p.raw.Name
	switch {
	case name == "":
		return p.formatErr("entrée sans nom")
	case p.raw.Dsize < 0:
		return p.formatErr(fmt.Sprintf("taille négative pour %q", name))
	case strings.IndexByte(name, 0) >= 0:
		return p.formatErr(fmt.Sprintf("nom invalide %q", name))
	case !isRoot && (name == "." || name == ".." || strings.IndexByte(name, '/') >= 0):
		return p.formatErr(fmt.Sprintf("nom d'entrée invalide %q", name))
	}
	return nil
}

// readEntry reads the next element of a directory array into p.raw and
// reports whether it is a directory (an array, whose information object
// has then been consumed) or a file (an object).
//
// The fast path decodes each information object with one Decode call.
// That requires knowing whether the next element is '{' or '[' before
// consuming it, which the token API cannot tell; the bytes the decoder has
// already buffered usually can. When they cannot (the element starts past
// the end of the buffer), the element is read with Token instead.
func (p *ncduReader) readEntry() (isDir bool, err error) {
	switch p.peekKind() {
	case '{':
		return false, p.decodeInfo()
	case '[':
		if err := p.expectDelim('['); err != nil {
			return false, err
		}
		return true, p.readDirInfo()
	case 0:
		t, err := p.token()
		if err != nil {
			return false, err
		}
		switch t {
		case json.Delim('{'):
			return false, p.tokenInfo()
		case json.Delim('['):
			return true, p.readDirInfo()
		}
	}
	return false, p.formatErr("objet (fichier) ou tableau (répertoire) attendu")
}

// readDirInfo reads the information object that starts a directory array
// whose '[' has just been consumed.
func (p *ncduReader) readDirInfo() error {
	if err := p.need("objet d'information du répertoire"); err != nil {
		return err
	}
	switch p.peekKind() {
	case '{':
		return p.decodeInfo()
	case 0:
		if err := p.expectDelim('{'); err != nil {
			return err
		}
		return p.tokenInfo()
	}
	return p.formatErr("un répertoire doit commencer par son objet d'information")
}

// peekKind returns the first byte of the next value from the decoder's
// buffer, skipping whitespace and one separating comma, or 0 if the buffer
// ends first. It must follow a More() that returned true, which guarantees
// the decoder has buffered at least the next non-space byte.
func (p *ncduReader) peekKind() byte {
	if p.tokensOnly {
		return 0
	}
	buf := p.dec.Buffered()
	comma := false
	for {
		n, _ := buf.Read(p.peek[:])
		if n == 0 {
			return 0
		}
		for _, c := range p.peek[:n] {
			switch c {
			case ' ', '\t', '\n', '\r':
			case ',':
				if comma {
					return c
				}
				comma = true
			default:
				return c
			}
		}
	}
}

// decodeInfo decodes the next value, an information object, into p.raw.
func (p *ncduReader) decodeInfo() error {
	p.raw = rawInfo{Dev: noDev}
	if err := p.dec.Decode(&p.raw); err != nil {
		return p.fail(err)
	}
	return nil
}

// composite stands for an object or array value already skipped by
// tokenInfo.
type composite struct{}

// tokenInfo reads an information object whose '{' has been consumed, with
// the same result as decodeInfo.
func (p *ncduReader) tokenInfo() error {
	p.raw = rawInfo{Dev: noDev}
	for p.dec.More() {
		kt, err := p.token()
		if err != nil {
			return err
		}
		key, _ := kt.(string)
		v, err := p.token()
		if err != nil {
			return err
		}
		if d, ok := v.(json.Delim); ok {
			if err := p.skipOpen(d); err != nil {
				return err
			}
			v = composite{}
		}
		if err := p.raw.set(key, v); err != nil {
			return p.formatErr(err.Error())
		}
	}
	return p.expectDelim('}')
}

// set assigns one key of an information object from a token value, with
// encoding/json's rules: case-insensitive keys, null leaves a scalar field
// unchanged, and a value of the wrong type is an error.
func (ri *rawInfo) set(key string, v any) error {
	field := func(name string) bool { return strings.EqualFold(key, name) }
	var err error
	switch {
	case field("excluded"):
		ri.Excluded = v
	case v == nil:
	case field("name"):
		s, ok := v.(string)
		if !ok {
			return typeError(key)
		}
		ri.Name = s
	case field("dsize"):
		ri.Dsize, err = tokenInt(v)
	case field("mtime"):
		ri.Mtime, err = tokenInt(v)
	case field("dev"):
		ri.Dev, err = tokenUint(v)
	case field("ino"):
		ri.Ino, err = tokenUint(v)
	case field("nlink"):
		ri.Nlink, err = tokenUint(v)
	case field("hlnkc"):
		ri.Hlnkc, err = tokenBool(v)
	case field("read_error"):
		ri.ReadError, err = tokenBool(v)
	}
	if err != nil {
		return typeError(key)
	}
	return nil
}

func typeError(key string) error {
	return fmt.Errorf("champ %q de type inattendu", key)
}

var errType = errors.New("type")

func tokenInt(v any) (int64, error) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, errType
	}
	return strconv.ParseInt(string(n), 10, 64)
}

func tokenUint(v any) (uint64, error) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, errType
	}
	return strconv.ParseUint(string(n), 10, 64)
}

func tokenBool(v any) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, errType
	}
	return b, nil
}

// truthy reports whether an "excluded" value marks the entry as excluded:
// anything but absent, null, false, "" or 0.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, err := x.Float64()
		return err != nil || f != 0
	}
	return true
}

// numberToInt converts a JSON number to an integer, accepting an integral
// float form such as 1.0 or 1e9.
func numberToInt(n json.Number) (int64, bool) {
	if i, err := n.Int64(); err == nil {
		return i, true
	}
	f, err := n.Float64()
	if err != nil || f != math.Trunc(f) || math.Abs(f) > 1<<62 {
		return 0, false
	}
	return int64(f), true
}

// integer reads the next value as an integer.
func (p *ncduReader) integer(what string) (int64, error) {
	if err := p.need(what); err != nil {
		return 0, err
	}
	t, err := p.token()
	if err != nil {
		return 0, err
	}
	n, ok := t.(json.Number)
	if !ok {
		return 0, p.formatErr(what + " : nombre attendu")
	}
	i, ok := numberToInt(n)
	if !ok {
		return 0, p.formatErr(what + " : entier attendu")
	}
	return i, nil
}

// need checks that the current array has another element, telling a
// truncated input from a short array otherwise.
func (p *ncduReader) need(what string) error {
	if p.dec.More() {
		return nil
	}
	if _, err := p.token(); err != nil {
		return err
	}
	return p.formatErr(what + " manquant")
}

// skipRest consumes the remaining elements of the current array and its
// closing bracket.
func (p *ncduReader) skipRest() error {
	return p.skipOpen(json.Delim('['))
}

// skipValue consumes one complete value.
func (p *ncduReader) skipValue() error {
	t, err := p.token()
	if err != nil {
		return err
	}
	if d, ok := t.(json.Delim); ok {
		return p.skipOpen(d)
	}
	return nil
}

// skipOpen consumes tokens up to and including the delimiter closing d,
// which has just been read.
func (p *ncduReader) skipOpen(d json.Delim) error {
	if d != '[' && d != '{' {
		return p.formatErr(fmt.Sprintf("délimiteur %q inattendu", d))
	}
	for depth := 1; depth > 0; {
		t, err := p.token()
		if err != nil {
			return err
		}
		switch t {
		case json.Delim('['), json.Delim('{'):
			depth++
		case json.Delim(']'), json.Delim('}'):
			depth--
		}
	}
	return nil
}

func (p *ncduReader) expectDelim(d json.Delim) error {
	t, err := p.token()
	if err != nil {
		return err
	}
	if t != d {
		return p.formatErr(fmt.Sprintf("%q attendu", d))
	}
	return nil
}

// token returns the next token; inside the document, EOF means truncation.
func (p *ncduReader) token() (json.Token, error) {
	t, err := p.dec.Token()
	if err != nil {
		return nil, p.fail(err)
	}
	return t, nil
}

// fail converts a decoder error into a ReadNcdu error.
func (p *ncduReader) fail(err error) error {
	if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("import ncdu : %w (fin des données à l'octet %d)", ErrTruncated, p.dec.InputOffset())
	}
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return fmt.Errorf("import ncdu : %w : JSON invalide à l'octet %d : %s",
			ErrFormat, se.Offset, strings.TrimPrefix(se.Error(), "json: "))
	}
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		return fmt.Errorf("import ncdu : %w : champ %q de type inattendu (%s) à l'octet %d",
			ErrFormat, te.Field, te.Value, te.Offset)
	}
	return fmt.Errorf("import ncdu : lecture : %w", err)
}

func (p *ncduReader) formatErr(msg string) error {
	return fmt.Errorf("import ncdu : %w : %s (octet %d)", ErrFormat, msg, p.dec.InputOffset())
}
