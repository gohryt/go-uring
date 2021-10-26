package main

import (
	"errors"
	"github.com/godzie44/go-uring/uring"
	"log"
	"os"
	"sync"
	"syscall"
)

type connType int

const (
	_ connType = iota
	ACCEPT
	READ
	WRITE
)

type connInfo struct {
	typ  connType
	fd   int
	buff []byte
}

var connMap = map[int]*connInfo{}

var response = []byte("pong\n")

func main() {
	socket, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	checkErr(err)
	defer syscall.Close(socket)

	checkErr(setDefaultListenerSockopts(socket))
	checkErr(setDefaultMulticastSockopts(socket))

	addr := syscall.SockaddrInet4{
		Port: 8080,
	}

	checkErr(syscall.Bind(socket, &addr))
	checkErr(syscall.Listen(socket, syscall.SOMAXCONN))

	ring, err := uring.NewRing(uring.MaxEntries)
	checkErr(err)
	defer ring.Close()

	if !ring.Params.FeatFastPoll() {
		checkErr(errors.New("IORING_FEAT_FAST_POLL not available"))
	}

	connMap[socket] = &connInfo{
		typ: ACCEPT,
		fd:  socket,
	}
	addAccept(ring, socket)

	for {
		if _, err := ring.Submit(); err != nil {
			checkErr(err)
		}

		_, err = ring.WaitCQEvents(1)
		if err == syscall.EAGAIN || err == syscall.EINTR {
			continue
		}

		checkErr(err)

		events := ring.PeekCQEventBatch(syscall.SOMAXCONN)
		handleEvents(ring, events)
	}
}

func handleEvents(ring *uring.URing, events []*uring.CQEvent) {
	for _, cqe := range events {
		info := connMap[int(cqe.UserData)]
		switch info.typ {
		case ACCEPT:
			checkErr(cqe.Error())

			connFd := cqe.Res
			ring.SeenCQE(cqe)

			addRead(ring, int(connFd))
			addAccept(ring, info.fd)
		case READ:
			if cqe.Error() != nil || cqe.Res == 0 {
				shutdown(info.fd)
			} else {
				addWrite(ring, info.fd, response)
			}
			ring.SeenCQE(cqe)
		case WRITE:
			checkErr(cqe.Error())

			ring.SeenCQE(cqe)
			addRead(ring, info.fd)
		}
	}
}

var buffPool = sync.Pool{New: func() interface{} {
	return make([]byte, 2048)
}}

func addAccept(ring *uring.URing, fd int) {
	checkErr(
		ring.QueueSQE(uring.Accept(uintptr(fd), 0), 0, uint64(fd)),
	)
}

func addRead(ring *uring.URing, fd int) {
	if _, exists := connMap[fd]; !exists {
		connMap[fd] = &connInfo{
			fd:   fd,
			buff: buffPool.Get().([]byte),
		}
	}
	connMap[fd].typ = READ

	vec := syscall.Iovec{Base: &connMap[fd].buff[0], Len: uint64(len(connMap[fd].buff))}
	checkErr(
		ring.QueueSQE(uring.Recv(uintptr(fd), vec, 0), 0, uint64(fd)),
	)
}

func addWrite(ring *uring.URing, fd int, data []byte) {
	connMap[fd].typ = WRITE

	vec := syscall.Iovec{Base: &data[0], Len: uint64(len(data))}
	checkErr(
		ring.QueueSQE(uring.Send(uintptr(fd), vec, 0), 0, uint64(fd)),
	)
}

func shutdown(fd int) {
	conn := connMap[fd]
	buffPool.Put(conn.buff)
	delete(connMap, fd)

	syscall.Shutdown(fd, syscall.SHUT_RDWR)
}

func setDefaultListenerSockopts(s int) error {
	return os.NewSyscallError("setsockopt", syscall.SetsockoptInt(s, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1))
}

func setDefaultMulticastSockopts(s int) error {
	return os.NewSyscallError("setsockopt", syscall.SetsockoptInt(s, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1))
}

func checkErr(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
