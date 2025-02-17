//go:build generate

// This program generates grapheme_break_test.go from the Unicode Character
// Database auxiliary data files at https://www.unicode.org/Public/
// Either directly via HTTP by URL or from a local copy of the file.
//
//go:generate go run gen_breaktest.go

package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
)

// See https://www.unicode.org/license.html for the Unicode license agreement.

// We want to test against a specific version rather than the latest, which
// can be found at:
// https://www.unicode.org/Public/UCD/latest/ucd/auxiliary/GraphemeBreakTest.txt
// When/if the package is upgraded to a new version, change these to generate
// new tests.
const (
	url      = `https://www.unicode.org/Public/14.0.0/ucd/auxiliary/GraphemeBreakTest.txt`
	filename = `GraphemeBreakTest-14.0.0.txt`
)

func main() {
	log.SetPrefix("gen_breaktest: ")
	log.SetFlags(0)

	// Read text of testcases and parse into Go source code.
	src, err := readAndParse()
	if err != nil {
		log.Fatal(err)
	}

	// Format the Go code.
	srcfmt, err := format.Source(src)
	if err != nil {
		log.Fatalln("gofmt:", err)
		//srcfmt = src
	}

	// Write it out.
	if err := ioutil.WriteFile("grapheme_break_test.go", srcfmt, 0644); err != nil {
		log.Fatal(err)
	}
}

// readAndParse reads a GraphemeBreakTest text file, either from a local file or
// from a URL.
//
// It parses the file data into Go source code representing the testcases.
func readAndParse() ([]byte, error) {
	var r io.ReadCloser
	if f, err := os.Open(filename); err == nil {
		log.Printf("using %q", filename)
		r = f
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else {
		log.Printf("using %q", url)
		resp, err := http.Get(url)
		if err != nil {
			return nil, err
		}
		r = resp.Body
	}
	defer r.Close()

	buf := new(bytes.Buffer)
	buf.Grow(120 << 10)
	buf.WriteString(`// Code generated via go generate from gen_breaktest.go. DO NOT EDIT.

package uniseg

// unicodeTestCases are Grapheme testcases taken from
// ` + url + `,
// see https://www.unicode.org/license.html for the Unicode license agreement.
var unicodeTestCases = []testCase {
`)

	sc := bufio.NewScanner(r)
	num := 1
	var line []byte
	if sc.Scan() {
		// Check first line for "# filename"
		line = sc.Bytes()
		if len(line) != 2+len(filename) || !strings.HasSuffix(string(line), filename) {
			return nil, fmt.Errorf(`line %d: exected "# %v", got %q`, num, filename, line)
		}
	}

	original := make([]byte, 0, 64)
	expected := make([]byte, 0, 64)
	for sc.Scan() {
		num++
		line = sc.Bytes()
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		var comment []byte
		if i := bytes.IndexByte(line, '#'); i >= 0 {
			comment = bytes.TrimSpace(line[i+1:])
			line = bytes.TrimSpace(line[:i])
		}
		original, expected, err := parseRuneSequence(line, original[:0], expected[:0])
		if err != nil {
			return nil, fmt.Errorf(`line %d: %v: %q`, num, err, line)
		}
		fmt.Fprintf(buf, "\t{original: \"%s\", expected: %s}, // %s\n", original, expected, comment)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	// Check for final "# EOF", useful check if we're streaming via HTTP
	if !bytes.Equal(line, []byte("# EOF")) {
		return nil, fmt.Errorf(`line %d: exected "# EOF" as final line, got %q`, num, line)
	}
	buf.WriteString("}\n")
	log.Printf("processed %d lines to %d/%d bytes", num, buf.Len(), buf.Cap())
	return buf.Bytes(), nil
}

// Used by parseRuneSequence to match input via bytes.HasPrefix.
var (
	prefix  = []byte("÷ ")
	breakOk = []byte("÷")
	breakNo = []byte("×")
)

// parseRuneSequence parses a rune + breaking opportunity sequence from b
// and appends the Go code for testcase.original to orig
// and appends the Go code for testcase.expected to exp.
// It retuns the new orig and exp slices.
//
// E.g. for the input b="÷ 0020 × 0308 ÷ 1F1E6 ÷"
// it will append
//     "\u0020\u0308\U0001F1E6"
// and "[][]rune{{0x0020,0x0308},{0x1F1E6},}"
// to orig and exp respectively.
//
// The formatting of exp is expected to be cleaned up by gofmt or format.Source.
// Note we explicitly require the sequence to start with ÷ and we implicitly
// require it to end with ÷.
func parseRuneSequence(b, orig, exp []byte) ([]byte, []byte, error) {
	// Check for and remove first ÷.
	if !bytes.HasPrefix(b, prefix) {
		return nil, nil, fmt.Errorf("expected line to start with %q", prefix)
	}
	b = b[len(prefix):]

	boundary := true
	exp = append(exp, "[][]rune{"...)
	for len(b) > 0 {
		if boundary {
			exp = append(exp, '{')
		}
		exp = append(exp, "0x"...)
		// Find end of hex digits.
		var i int
		for i = 0; i < len(b) && b[i] != ' '; i++ {
			if d := b[i]; ('0' <= d || d <= '9') ||
				('A' <= d || d <= 'F') ||
				('a' <= d || d <= 'f') {
				continue
			}
			return nil, nil, errors.New("bad hex digit")
		}
		switch i {
		case 4:
			orig = append(orig, "\\u"...)
		case 5:
			orig = append(orig, "\\U000"...)
		default:
			return nil, nil, errors.New("unsupport code point hex length")
		}
		orig = append(orig, b[:i]...)
		exp = append(exp, b[:i]...)
		b = b[i:]

		// Check for space between hex and ÷ or ×.
		if len(b) < 1 || b[0] != ' ' {
			return nil, nil, errors.New("bad input")
		}
		b = b[1:]

		// Check for next boundary.
		switch {
		case bytes.HasPrefix(b, breakOk):
			boundary = true
			b = b[len(breakOk):]
		case bytes.HasPrefix(b, breakNo):
			boundary = false
			b = b[len(breakNo):]
		default:
			return nil, nil, errors.New("missing ÷ or ×")
		}
		if boundary {
			exp = append(exp, '}')
		}
		exp = append(exp, ',')
		if len(b) > 0 && b[0] == ' ' {
			b = b[1:]
		}
	}
	exp = append(exp, '}')
	return orig, exp, nil
}
