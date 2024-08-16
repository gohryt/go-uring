//go:build linux

package uring

import (
	"net"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var str = "This is a test of sendmsg and recvmsg over io_uring!"

func TestSendRecv(t *testing.T) {
	mu := &sync.Mutex{}
	mu.Lock()
	cond := sync.NewCond(mu)

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		recv(t, cond)
	}()
	cond.Wait()

	send(t)

	wg.Wait()
}

func send(t *testing.T) {
	ring, err := New(1)
	require.NoError(t, err)
	defer ring.Close()

	conn, err := net.Dial("udp", "127.0.0.1:8087")
	require.NoError(t, err)
	defer conn.Close()

	f, err := conn.(*net.UDPConn).File()
	require.NoError(t, err)

	require.NoError(t, ring.QueueSQE(Send(f.Fd(), []byte(str), 0), 0, 1))

	_, err = ring.SubmitAndWaitCQEvents(1)
	require.NoError(t, err)
}

func recv(t *testing.T, cond *sync.Cond) {
	ring, err := New(1)
	require.NoError(t, err)
	defer ring.Close()

	pc, err := net.ListenPacket("udp", ":8087")
	require.NoError(t, err)
	defer pc.Close()

	f, err := pc.(*net.UDPConn).File()
	require.NoError(t, err)

	buff := make([]byte, 128)
	require.NoError(t, ring.QueueSQE(Recv(f.Fd(), buff, 0), 0, 2))

	_, err = ring.Submit()
	require.NoError(t, err)

	cond.Signal()

	cqe, err := ring.WaitCQEvents(1)
	require.NoError(t, err)

	if cqe.Error() == syscall.EINVAL {
		t.Skipf("Skipped, recv not supported on this kernel")
	}
	require.NoError(t, cqe.Error())

	assert.Equal(t, cqe.Res, int32(len(str)))
	assert.Equal(t, []byte(str), buff[:len(str)])
}
