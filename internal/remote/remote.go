package remote

import (
	"io"
	"net"
	"os"
	"strings"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"github.com/D1nma/disk_check/internal/assets"
)

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
