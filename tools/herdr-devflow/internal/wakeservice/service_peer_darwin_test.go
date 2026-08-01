//go:build darwin

package wakeservice

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDarwinPeerUIDComesFromKernelCredentials(t *testing.T) {
	socket := filepath.Join(shortTempDir(t), "peer.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	acceptErrors := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			acceptErrors <- err
			return
		}
		accepted <- connection
	}()
	client, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var server net.Conn
	select {
	case server = <-accepted:
	case err := <-acceptErrors:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("timed out accepting peer test connection")
	}
	defer server.Close()
	uid, err := peerUID(server)
	if err != nil {
		t.Fatal(err)
	}
	if uid != os.Getuid() {
		t.Fatalf("peer uid = %d, want process uid %d", uid, os.Getuid())
	}
}
