package expect

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"unicode/utf8"
)

type buffer struct {
	file  *os.File
	cache bytes.Buffer
	debug bool
}

func (b *buffer) read(chunk []byte) (int, error) {
	if b.cache.Len() > 0 {
		n, _ := b.cache.Read(chunk)
		if b.debug {
			fmt.Printf("\x1b[36;1mREAD:|>%s<|\x1b[0m\r\n", string(chunk[:n]))
			fmt.Printf("\x1b[36;1mREAD:|>%v<|\x1b[0m\r\n", chunk[:n])
		}
		return n, nil
	}

	n, err := b.file.Read(chunk) // this may be blocked
	if err != nil {
		if e, ok := err.(*os.PathError); ok && e.Err == syscall.EIO {
			// It's just the PTY telling us that it closed all good
			// See: https://github.com/buildbox/agent/pull/34#issuecomment-46080419
			err = io.EOF
		}
	}
	if b.debug {
		fmt.Printf("\x1b[34;1m|>%s<|\x1b[0m\r\n", string(chunk[:n]))
		fmt.Printf("\x1b[34;1m|>%v<|\x1b[0m\r\n", chunk[:n])
		f, err := os.OpenFile("/tmp/expect_stream_data",
			os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		if _, err = f.WriteString(string(chunk[:n])); err != nil {
			panic(err)
		}
	}
	return n, err
}

func (b *buffer) unread(chunk []byte) {
	if len(chunk) == 0 {
		return
	}

	if b.debug {
		fmt.Printf("\x1b[35;1mUNREAD:|>%s<|\x1b[0m\r\n", string(chunk))
		fmt.Printf("\x1b[35;1mUNREAD:|>%v<|\x1b[0m\r\n", chunk)
	}
	if b.cache.Len() == 0 {
		b.cache.Write(chunk)
		return
	}

	d := make([]byte, 0, len(chunk)+b.cache.Len())
	d = append(d, chunk...)
	d = append(d, b.cache.Bytes()...)
	b.cache.Reset()
	b.cache.Write(d)
}

func (b *buffer) ReadRune() (rune, int, error) {
	chunk := make([]byte, utf8.UTFMax)
	for n := 0; n < utf8.UTFMax; {
		n, err := b.read(chunk[n:])
		if utf8.FullRune(chunk[:n]) {
			r, rL := utf8.DecodeRune(chunk)
			if n > rL {
				b.unread(chunk[rL:n])
			}
			return r, rL, err
		}
	}
	return 0, 0, errors.New("file is not a valid UTF=8 encoding")
}
