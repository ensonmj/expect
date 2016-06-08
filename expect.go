package expect

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"syscall"
	"unicode/utf8"

	shell "github.com/kballard/go-shellquote"
	"github.com/kr/pty"
)

type buffer struct {
	file  *os.File
	cache bytes.Buffer
	debug bool
}

func (b *buffer) read(chunk []byte) (int, error) {
	nread := 0
	if b.cache.Len() > 0 {
		n, _ := b.cache.Read(chunk)
		if b.debug {
			fmt.Printf("\x1b[36;1mREAD:|>%s<|\x1b[0m\r\n", string(chunk[:n]))
			fmt.Printf("\x1b[36;1mREAD:|>%v<|\x1b[0m\r\n", chunk[:n])
		}
		if n == len(chunk) {
			return n, nil
		}
		nread = n
	}
	fn, err := b.file.Read(chunk[nread:]) // this may be blocked
	if err != nil {
		if e, ok := err.(*os.PathError); ok && e.Err == syscall.EIO {
			// It's just the PTY telling us that it closed all good
			// See: https://github.com/buildbox/agent/pull/34#issuecomment-46080419
			err = io.EOF
		}
	}
	if b.debug {
		fmt.Printf("\x1b[34;1m|>%s<|\x1b[0m\r\n", string(chunk[nread:fn+nread]))
		fmt.Printf("\x1b[34;1m|>%v<|\x1b[0m\r\n", chunk[nread:fn+nread])
		// f, err := os.OpenFile("/tmp/expect_stream_data",
		// 	os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		// if err != nil {
		// 	panic(err)
		// }
		// defer f.Close()
		// if _, err = f.WriteString(string(chunk[nread : fn+nread])); err != nil {
		// 	panic(err)
		// }
	}
	return fn + nread, err
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
	l := b.cache.Len()

	chunk := make([]byte, utf8.UTFMax)
	if l > 0 {
		n, err := b.cache.Read(chunk)
		if err != nil {
			return 0, 0, err
		}
		if utf8.FullRune(chunk[:n]) {
			r, rL := utf8.DecodeRune(chunk)
			if n > rL {
				b.unread(chunk[rL:n])
			}
			return r, rL, nil
		}
	}
	// else add bytes from the file, then try that
	for l < utf8.UTFMax {
		fn, err := b.file.Read(chunk[l : l+1])
		if err != nil {
			return 0, 0, err
		}
		l = l + fn
		if utf8.FullRune(chunk[:l]) {
			r, rL := utf8.DecodeRune(chunk)
			return r, rL, nil
		}
	}
	return 0, 0, errors.New("File is not a valid UTF=8 encoding")
}

type ExpectSubproc struct {
	cmd *exec.Cmd
	buf *buffer
}

func Spawn(command string) (*ExpectSubproc, error) {
	splitArgs, err := shell.Split(command)
	if err != nil {
		return nil, err
	}
	numArgs := len(splitArgs) - 1
	if numArgs < 0 {
		return nil, errors.New("expect: No command given to spawn")
	}
	path, err := exec.LookPath(splitArgs[0])
	if err != nil {
		return nil, err
	}

	proc := new(ExpectSubproc)
	if numArgs >= 1 {
		proc.cmd = exec.Command(path, splitArgs[1:]...)
	} else {
		proc.cmd = exec.Command(path)
	}
	proc.buf = new(buffer)

	f, err := pty.Start(proc.cmd)
	if err != nil {
		return nil, err
	}
	proc.buf.file = f

	return proc, nil
}

func (e *ExpectSubproc) Debug(open bool) {
	e.buf.debug = open
}

func (e *ExpectSubproc) Close() error {
	if err := e.cmd.Process.Kill(); err != nil {
		return err
	}
	if err := e.buf.file.Close(); err != nil {
		return err
	}
	return nil
}

func (e *ExpectSubproc) Wait() error {
	return e.cmd.Wait()
}

func (e *ExpectSubproc) Interact() {
	defer e.cmd.Wait()
	io.Copy(os.Stdout, &e.buf.cache)
	go io.Copy(os.Stdout, e.buf.file)
	go io.Copy(e.buf.file, os.Stdin)
}

func (e *ExpectSubproc) SendLine(cmd string) error {
	return e.Send(cmd + "\r\n")
}

func (e *ExpectSubproc) Send(cmd string) error {
	_, err := io.WriteString(e.buf.file, cmd)
	return err
}

func (e *ExpectSubproc) ReadLine() (string, error) {
	str, err := e.ReadUntil('\n')
	return string(str), err
}

func (e *ExpectSubproc) ReadUntil(delim byte) ([]byte, error) {
	all := make([]byte, 0, 1024)
	chunk := make([]byte, 128)
	for {
		n, err := e.buf.read(chunk)
		for i := 0; i < n; i++ {
			if chunk[i] == delim {
				// if len(chunk) > i+1 {
				if i+1 < n {
					e.buf.unread(chunk[i+1 : n])
				}
				all = append(all, chunk[:i+1]...)
				return all, err
			}
		}
		all = append(all, chunk[:n]...)

		if err != nil {
			return all, err
		}
	}
}

func (e *ExpectSubproc) AsyncInteractChannels() (chan<- string, <-chan string) {
	send := make(chan string)
	receive := make(chan string)

	go func() {
		for {
			str, err := e.ReadLine()
			if err != nil {
				close(receive)
				return
			}
			receive <- str
		}
	}()

	go func() {
		for {
			select {
			case sendCmd, ok := <-send:
				if !ok {
					return
				}
				err := e.Send(sendCmd)
				if err != nil {
					receive <- "expect inner err: " + err.Error()
					return
				}
			}
		}
	}()

	return send, receive
}

func (e *ExpectSubproc) Expect(searchStr string) error {
	num := len(searchStr)
	if num == 0 {
		return errors.New("search string is empty")
	}

	chunk := make([]byte, num*2)
	table := buildKMPTable(searchStr)
	chunkIndex := 0
	strIndex := 0
	for {
		n, err := e.buf.read(chunk)
		offset := chunkIndex + strIndex
		for chunkIndex+strIndex-offset < n {
			if searchStr[strIndex] == chunk[chunkIndex+strIndex-offset] {
				strIndex += 1
				if strIndex == num {
					unreadIndex := chunkIndex + strIndex - offset
					// if len(chunk) > unreadIndex {
					if unreadIndex < n {
						e.buf.unread(chunk[unreadIndex:n])
					}
					return nil
				}
			} else {
				chunkIndex += strIndex - table[strIndex]
				if table[strIndex] > -1 {
					strIndex = table[strIndex]
				} else {
					strIndex = 0
				}
			}
		}
		if err != nil {
			return err
		}
	}
}

func buildKMPTable(searchStr string) []int {
	length := len(searchStr)
	if length < 2 {
		length = 2
	}
	table := make([]int, length)
	table[0] = -1
	table[1] = 0

	pos := 2
	cnd := 0
	for pos < len(searchStr) {
		if searchStr[pos-1] == searchStr[cnd] {
			cnd += 1
			table[pos] = cnd
			pos += 1
		} else if cnd > 0 {
			cnd = table[cnd]
		} else {
			table[pos] = 0
			pos += 1
		}
	}

	return table
}

// ExpectMatch checks whether a textual regular expression matches happened
func (e *ExpectSubproc) ExpectMatch(regex string) (bool, error) {
	return regexp.MatchReader(regex, e.buf)
}
