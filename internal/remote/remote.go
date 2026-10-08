package remote

import (
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"

	"github.com/D1nma/disk_check/internal/assets"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

var (
	hostRegexp = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
	userRegexp = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

// ValidateHost validates that a hostname/IP contains only safe characters.
func ValidateHost(host string) error {
	if host == "" {
		return fmt.Errorf("host cannot be empty")
	}
	if !hostRegexp.MatchString(host) {
		return fmt.Errorf("invalid host %q", host)
	}
	return nil
}

// ValidateUser validates that a username contains only safe characters.
func ValidateUser(user string) error {
	if user == "" {
		return fmt.Errorf("user cannot be empty")
	}
	if !userRegexp.MatchString(user) {
		return fmt.Errorf("invalid user %q", user)
	}
	return nil
}

// remoteReader wraps an io.Reader and closes the SSH session and client when closed.
type remoteReader struct {
	io.Reader
	session *ssh.Session
	client  *ssh.Client
}

func (r *remoteReader) Close() error {
	r.session.Close()
	return r.client.Close()
}

func RunRemote(host string, user string) (io.ReadCloser, error) {
	if err := ValidateHost(host); err != nil {
		return nil, err
	}
	if err := ValidateUser(user); err != nil {
		return nil, err
	}

	// 1. Setup Auth
	var auths []ssh.AuthMethod
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if agentChan, err := net.Dial("unix", sock); err == nil {
			auths = append(auths, ssh.PublicKeysCallback(agent.NewClient(agentChan).Signers))
		}
	}

	config := &ssh.ClientConfig{
		User: user,
		Auth: auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", host+":22", config)
	if err != nil {
		return nil, err
	}

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, err
	}

	// 2. Run embedded script
	session.Stdin = strings.NewReader(assets.BashScript)
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		client.Close()
		return nil, err
	}

	// Execute bash -s which reads from stdin
	err = session.Start("bash -s")
	if err != nil {
		session.Close()
		client.Close()
		return nil, err
	}

	return &remoteReader{
		Reader:  stdout,
		session: session,
		client:  client,
	}, nil
}
