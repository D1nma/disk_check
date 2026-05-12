package remote

import (
	"io"
	"golang.org/x/crypto/ssh"
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
	// 1. Setup Auth (simplified for plan, real impl will look for keys)
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{}, // TODO: Add key/agent auth
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
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		client.Close()
		return nil, err
	}

	// Pass the script content as a string
	err = session.Start(string(assets.BashScript))
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
