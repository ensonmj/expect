package expect

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"syscall"
	"unsafe"

	shell "github.com/kballard/go-shellquote"
	"github.com/kr/pty"
	"golang.org/x/crypto/ssh/terminal"
)

type ExpectSubproc struct {
	cmd      *exec.Cmd
	buf      *buffer
	stdinBuf *buffer
}

func Command(cmd string) (*ExpectSubproc, error) {
	splitArgs, err := shell.Split(cmd)
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
	proc.stdinBuf = &buffer{file: os.Stdin}

	return proc, nil
}

func (e *ExpectSubproc) Start() error {
	f, err := pty.Start(e.cmd)
	if err != nil {
		return err
	}
	e.buf.file = f

	// Set pty size
	pty.InheritSize(os.Stdin, f)
	// Auto adjust pty size
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for range ch {
			pty.InheritSize(os.Stdin, f)
		}
	}()

	return nil
}

func Spawn(cmd string) (*ExpectSubproc, error) {
	proc, err := Command(cmd)
	if err != nil {
		return nil, err
	}
	return proc, proc.Start()
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
	// Set stdin in raw mode.
	oldState, err := terminal.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		panic(err)
	}
	defer terminal.Restore(int(os.Stdin.Fd()), oldState)

	defer e.cmd.Wait()
	io.Copy(os.Stdout, &e.buf.cache)
	go io.Copy(os.Stdout, e.buf.file)
	go io.Copy(e.buf.file, os.Stdin)
}

func (e *ExpectSubproc) SendLine(cmd string) error {
	return e.Send(cmd + "\r\n")
}

func (e *ExpectSubproc) SendLineUser(cmd string) error {
	return e.SendUser(cmd + "\r\n")
}

func (e *ExpectSubproc) Send(cmd string) error {
	_, err := io.WriteString(e.buf.file, cmd)
	return err
}

func (e *ExpectSubproc) SendUser(cmd string) error {
	_, err := io.WriteString(os.Stdout, cmd)
	return err
}

func (e *ExpectSubproc) ReadLine() (string, error) {
	str, err := e.ReadUntil('\n')
	return string(str), err
}

func (e *ExpectSubproc) ReadLineUser() (string, error) {
	str, err := e.ReadUntilUser('\n')
	return string(str), err
}

func (e *ExpectSubproc) ReadUntil(delim byte) ([]byte, error) {
	return doReadUnitl(delim, e.buf)
}

// ReadUnitlUser read content from stdin
func (e *ExpectSubproc) ReadUntilUser(delim byte) ([]byte, error) {
	return doReadUnitl(delim, e.stdinBuf)
}

func doReadUnitl(delim byte, buf *buffer) ([]byte, error) {
	all := make([]byte, 0, 1024)
	chunk := make([]byte, 128)
	for {
		n, err := buf.read(chunk)
		for i := 0; i < n; i++ {
			if chunk[i] == delim {
				// if len(chunk) > i+1 {
				if i+1 < n {
					buf.unread(chunk[i+1 : n])
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

func (e *ExpectSubproc) Expect(searchStr string) error {
	return doExpect(searchStr, e.buf)
}

// ExpectUser read content from stdin and test match
func (e *ExpectSubproc) ExpectUser(searchStr string) error {
	return doExpect(searchStr, e.stdinBuf)
}

func doExpect(searchStr string, buf *buffer) error {
	num := len(searchStr)
	if num == 0 {
		return errors.New("search string is empty")
	}

	chunk := make([]byte, num*2)
	table := buildKMPTable(searchStr)
	chunkIndex := 0
	strIndex := 0
	for {
		n, err := buf.read(chunk)
		offset := chunkIndex + strIndex
		for chunkIndex+strIndex-offset < n {
			if searchStr[strIndex] == chunk[chunkIndex+strIndex-offset] {
				strIndex += 1
				if strIndex == num {
					unreadIndex := chunkIndex + strIndex - offset
					// if len(chunk) > unreadIndex {
					if unreadIndex < n {
						buf.unread(chunk[unreadIndex:n])
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

type ExpectPair struct {
	SearchStr string
	Action    func(data []byte) error
}

func (e *ExpectSubproc) ExpectMulti(pairs []ExpectPair) error {
	return doExpectMulti(pairs, e.buf)
}

// ExpectMultiUser read content from stdin and test match
func (e *ExpectSubproc) ExpectMultiUser(pairs []ExpectPair) error {
	return doExpectMulti(pairs, e.stdinBuf)
}

func doExpectMulti(pairs []ExpectPair, buf *buffer) error {
	var cache bytes.Buffer
	var validPairs []ExpectPair
	var tables [][]int
	var chunkIndexs []int
	var strIndexs []int
	maxLen := 0
	for _, pair := range pairs {
		num := len(pair.SearchStr)
		if num > 0 {
			// SearchStr must be not empty, but Action can be nil
			validPairs = append(validPairs, pair)

			tables = append(tables, buildKMPTable(pair.SearchStr))
			chunkIndexs = append(chunkIndexs, 0)
			strIndexs = append(strIndexs, 0)

			if num > maxLen {
				maxLen = num
			}
		}
	}
	chunk := make([]byte, maxLen*2)

	for {
		n, err := buf.read(chunk)
		if n <= 0 {
			return err
		}

		for i, pair := range validPairs {
			searchStr := pair.SearchStr
			num := len(pair.SearchStr)
			offset := chunkIndexs[i] + strIndexs[i]
			for chunkIndexs[i]+strIndexs[i]-offset < n {
				if searchStr[strIndexs[i]] == chunk[chunkIndexs[i]+strIndexs[i]-offset] {
					strIndexs[i] += 1
					if strIndexs[i] == num {
						unreadIndex := chunkIndexs[i] + strIndexs[i] - offset
						if unreadIndex < n {
							buf.unread(chunk[unreadIndex:n])
						}
						if pair.Action != nil {
							cache.Write(chunk[:unreadIndex])
							return pair.Action(cache.Bytes())
						}
						return nil
					}
				} else {
					chunkIndexs[i] += strIndexs[i] - tables[i][strIndexs[i]]
					if tables[i][strIndexs[i]] > -1 {
						strIndexs[i] = tables[i][strIndexs[i]]
					} else {
						strIndexs[i] = 0
					}
				}
			}
			if err != nil {
				return err
			}
		}

		cache.Write(chunk)
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

// ExpectFind returns a slice holding
// func (e *ExpectSubproc) ExpectFind(regex string) ([]string, string, error) {
// 	re, err := regexp.Compile(regex)
// 	if err != nil {
// 		return nil, "", err
// 	}
// 	pairs := re.FindReaderSubmatchIndex(e.buf)
// 	l := len(pairs)
// 	numPairs := l / 2
// 	result := make([]string, numPairs)
// 	for i := 0; i < numPairs; i++ {
// 		result[i] = string
// 	}
// }

func (e *ExpectSubproc) Debug(open bool) {
	e.buf.debug = open
}

type winsize struct {
	Height uint16
	Width  uint16
	xpixel uint16 //unused
	ypixel uint16 //unused
}

func (e *ExpectSubproc) GetWinsize() (uint16, uint16, error) {
	ws := &winsize{}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(syscall.Stdin),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(ws))); errno != 0 {
		return 0, 0, errno
	}
	return ws.Height, ws.Width, nil
}

func (e *ExpectSubproc) SetWinsize(height, width uint16) error {
	ws := &winsize{Height: height, Width: width}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		e.buf.file.Fd(),
		uintptr(syscall.TIOCSWINSZ),
		uintptr(unsafe.Pointer(ws))); errno != 0 {
		fmt.Println(errno)
		return errno
	}
	return nil
}
