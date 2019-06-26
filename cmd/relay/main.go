package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/ensonmj/expect"
)

// Host config
type Host struct {
	HostName string
	User     string
	Pass     string
	Opt      string
}

// Conf for relay.toml
type Conf struct {
	Relay     string
	RelayUser string
	RelayPass string
	Hosts     map[string]Host
}

var (
	fVerbose bool
)

func init() {
	flag.BoolVar(&fVerbose, "v", false, "open debug log")
}

func main() {
	var conf Conf
	if _, err := toml.DecodeFile("relay.toml", &conf); err != nil {
		fmt.Println(err)
		return
	}

	flag.Parse()
	abbr := flag.Arg(0)
	remoteCmd := flag.Arg(1)
	host, ok := conf.Hosts[abbr]
	if !ok {
		errExit(fmt.Errorf("can't find %s\n from config", abbr))
	}

	cmd := fmt.Sprintf("zssh -t %s@%s", conf.RelayUser, conf.Relay)
	child, err := expect.Spawn(cmd)
	if err != nil {
		errExit(err)
	}
	if fVerbose {
		child.Debug(true)
	}

	err = child.Expect("password")
	if err != nil {
		errExit(err)
	}
	err = expect.WithoutEcho(func() error {
		pass := conf.RelayPass
		if pass == "" {
			err := child.SendLineUser("\nPlease enter your password:")
			if err != nil {
				return err
			}
			pass, err = child.ReadLineUser()
			if err != nil {
				return err
			}
		}
		return child.SendLine(pass)
	})
	if err != nil {
		errExit(err)
	}
	child.SendLineUser("Check password...")

	child.Expect("bash-baidu-ssl$")
	err = loginOrRun(child, host, remoteCmd)
	if err != nil {
		errExit(err)
	}
}

func loginOrRun(child *expect.ExpectSubproc, host Host, remoteCmd string) error {
	var cmd string
	if host.User != "" {
		cmd = fmt.Sprintf("ssh %s %s@%s", host.Opt, host.User, host.HostName)
	} else {
		cmd = fmt.Sprintf("ssh %s %s", host.Opt, host.HostName)
	}
	if remoteCmd != "" {
		cmd = fmt.Sprintf("%s %s", cmd, remoteCmd)
	}
	fmt.Println(cmd)
	err := child.SendLine(cmd)
	if err != nil {
		return err
	}

	if host.Pass != "" {
		err := child.Expect("password")
		if err != nil {
			return err
		}
		err = child.SendLine(host.Pass)
		if err != nil {
			return err
		}
	}

	if remoteCmd == "" {
		child.Interact()
	}

	return nil
}

func errExit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}