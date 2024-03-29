// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build darwin freebsd linux netbsd openbsd

package net

import (
	"io"
	"os"
	"sync"
	"syscall"
	"time"
)

// Network file descriptor.
type netFD struct {
	// locking/lifetime of sysfd
	sysmu   sync.Mutex
	sysref  int
	closing bool

	// immutable until Close
	sysfd       int
	family      int
	sotype      int
	isConnected bool
	sysfile     *os.File
	cr          chan bool
	cw          chan bool
	net         string
	laddr       Addr
	raddr       Addr

	// owned by client
	rdeadline int64
	rio       sync.Mutex
	wdeadline int64
	wio       sync.Mutex

	// owned by fd wait server
	ncr, ncw int
}

type InvalidConnError struct{}

func (e *InvalidConnError) Error() string   { return "invalid net.Conn" }
func (e *InvalidConnError) Temporary() bool { return false }
func (e *InvalidConnError) Timeout() bool   { return false }

// A pollServer helps FDs determine when to retry a non-blocking
// read or write after they get EAGAIN.  When an FD needs to wait,
// send the fd on s.cr (for a read) or s.cw (for a write) to pass the
// request to the poll server.  Then receive on fd.cr/fd.cw.
// When the pollServer finds that i/o on FD should be possible
// again, it will send fd on fd.cr/fd.cw to wake any waiting processes.
// This protocol is implemented as s.WaitRead() and s.WaitWrite().
//
// There is one subtlety: when sending on s.cr/s.cw, the
// poll server is probably in a system call, waiting for an fd
// to become ready.  It's not looking at the request channels.
// To resolve this, the poll server waits not just on the FDs it has
// been given but also its own pipe.  After sending on the
// buffered channel s.cr/s.cw, WaitRead/WaitWrite writes a
// byte to the pipe, causing the pollServer's poll system call to
// return.  In response to the pipe being readable, the pollServer
// re-polls its request channels.
//
// Note that the ordering is "send request" and then "wake up server".
// If the operations were reversed, there would be a race: the poll
// server might wake up and look at the request channel, see that it
// was empty, and go back to sleep, all before the requester managed
// to send the request.  Because the send must complete before the wakeup,
// the request channel must be buffered.  A buffer of size 1 is sufficient
// for any request load.  If many processes are trying to submit requests,
// one will succeed, the pollServer will read the request, and then the
// channel will be empty for the next process's request.  A larger buffer
// might help batch requests.
//
// To avoid races in closing, all fd operations are locked and
// refcounted. when netFD.Close() is called, it calls syscall.Shutdown
// and sets a closing flag. Only when the last reference is removed
// will the fd be closed.

type pollServer struct {
	cr, cw     chan *netFD // buffered >= 1
	pr, pw     *os.File
	poll       *pollster // low-level OS hooks
	sync.Mutex           // controls pending and deadline
	pending    map[int]*netFD
	deadline   int64 // next deadline (nsec since 1970)
}

func (s *pollServer) AddFD(fd *netFD, mode int) {
	intfd := fd.sysfd
	if intfd < 0 {
		// fd closed underfoot
		if mode == 'r' {
			fd.cr <- true
		} else {
			fd.cw <- true
		}
		return
	}

	s.Lock()

	var t int64
	key := intfd << 1
	if mode == 'r' {
		fd.ncr++
		t = fd.rdeadline
	} else {
		fd.ncw++
		key++
		t = fd.wdeadline
	}
	s.pending[key] = fd
	doWakeup := false
	if t > 0 && (s.deadline == 0 || t < s.deadline) {
		s.deadline = t
		doWakeup = true
	}

	wake, err := s.poll.AddFD(intfd, mode, false)
	if err != nil {
		panic("pollServer AddFD " + err.Error())
	}
	if wake {
		doWakeup = true
	}

	s.Unlock()

	if doWakeup {
		s.Wakeup()
	}
}

var wakeupbuf [1]byte

func (s *pollServer) Wakeup() { s.pw.Write(wakeupbuf[0:]) }

func (s *pollServer) LookupFD(fd int, mode int) *netFD {
	key := fd << 1
	if mode == 'w' {
		key++
	}
	netfd, ok := s.pending[key]
	if !ok {
		return nil
	}
	delete(s.pending, key)
	return netfd
}

func (s *pollServer) WakeFD(fd *netFD, mode int) {
	if mode == 'r' {
		for fd.ncr > 0 {
			fd.ncr--
			fd.cr <- true
		}
	} else {
		for fd.ncw > 0 {
			fd.ncw--
			fd.cw <- true
		}
	}
}

func (s *pollServer) Now() int64 {
	return time.Now().UnixNano()
}

func (s *pollServer) CheckDeadlines() {
	now := s.Now()
	// TODO(rsc): This will need to be handled more efficiently,
	// probably with a heap indexed by wakeup time.

	var next_deadline int64
	for key, fd := range s.pending {
		var t int64
		var mode int
		if key&1 == 0 {
			mode = 'r'
		} else {
			mode = 'w'
		}
		if mode == 'r' {
			t = fd.rdeadline
		} else {
			t = fd.wdeadline
		}
		if t > 0 {
			if t <= now {
				delete(s.pending, key)
				if mode == 'r' {
					s.poll.DelFD(fd.sysfd, mode)
					fd.rdeadline = -1
				} else {
					s.poll.DelFD(fd.sysfd, mode)
					fd.wdeadline = -1
				}
				s.WakeFD(fd, mode)
			} else if next_deadline == 0 || t < next_deadline {
				next_deadline = t
			}
		}
	}
	s.deadline = next_deadline
}

func (s *pollServer) Run() {
	var scratch [100]byte
	s.Lock()
	defer s.Unlock()
	for {
		var t = s.deadline
		if t > 0 {
			t = t - s.Now()
			if t <= 0 {
				s.CheckDeadlines()
				continue
			}
		}
		fd, mode, err := s.poll.WaitFD(s, t)
		if err != nil {
			print("pollServer WaitFD: ", err.Error(), "\n")
			return
		}
		if fd < 0 {
			// Timeout happened.
			s.CheckDeadlines()
			continue
		}
		if fd == s.pr.Fd() {
			// Drain our wakeup pipe (we could loop here,
			// but it's unlikely that there are more than
			// len(scratch) wakeup calls).
			s.pr.Read(scratch[0:])
			s.CheckDeadlines()
		} else {
			netfd := s.LookupFD(fd, mode)
			if netfd == nil {
				print("pollServer: unexpected wakeup for fd=", fd, " mode=", string(mode), "\n")
				continue
			}
			s.WakeFD(netfd, mode)
		}
	}
}

func (s *pollServer) WaitRead(fd *netFD) {
	s.AddFD(fd, 'r')
	<-fd.cr
}

func (s *pollServer) WaitWrite(fd *netFD) {
	s.AddFD(fd, 'w')
	<-fd.cw
}

// Network FD methods.
// All the network FDs use a single pollServer.

var pollserver *pollServer
var onceStartServer sync.Once

func startServer() {
	p, err := newPollServer()
	if err != nil {
		print("Start pollServer: ", err.Error(), "\n")
	}
	pollserver = p
}

func newFD(fd, family, sotype int, net string) (f *netFD, err error) {
	onceStartServer.Do(startServer)
	if e := syscall.SetNonblock(fd, true); e != nil {
		return nil, e
	}
	f = &netFD{
		sysfd:  fd,
		family: family,
		sotype: sotype,
		net:    net,
	}
	f.cr = make(chan bool, 1)
	f.cw = make(chan bool, 1)
	return f, nil
}

func (fd *netFD) setAddr(laddr, raddr Addr) {
	fd.laddr = laddr
	fd.raddr = raddr
	var ls, rs string
	if laddr != nil {
		ls = laddr.String()
	}
	if raddr != nil {
		rs = raddr.String()
	}
	fd.sysfile = os.NewFile(fd.sysfd, fd.net+":"+ls+"->"+rs)
}

func (fd *netFD) connect(ra syscall.Sockaddr) (err error) {
	err = syscall.Connect(fd.sysfd, ra)
	if err == syscall.EINPROGRESS {
		pollserver.WaitWrite(fd)
		var e int
		e, err = syscall.GetsockoptInt(fd.sysfd, syscall.SOL_SOCKET, syscall.SO_ERROR)
		if err != nil {
			return os.NewSyscallError("getsockopt", err)
		}
		if e != 0 {
			err = syscall.Errno(e)
		}
	}
	return err
}

// Add a reference to this fd.
func (fd *netFD) incref() {
	fd.sysmu.Lock()
	fd.sysref++
	fd.sysmu.Unlock()
}

// Remove a reference to this FD and close if we've been asked to do so (and
// there are no references left.
func (fd *netFD) decref() {
	fd.sysmu.Lock()
	fd.sysref--
	if fd.closing && fd.sysref == 0 && fd.sysfd >= 0 {
		// In case the user has set linger, switch to blocking mode so
		// the close blocks.  As long as this doesn't happen often, we
		// can handle the extra OS processes.  Otherwise we'll need to
		// use the pollserver for Close too.  Sigh.
		syscall.SetNonblock(fd.sysfd, false)
		fd.sysfile.Close()
		fd.sysfile = nil
		fd.sysfd = -1
	}
	fd.sysmu.Unlock()
}

func (fd *netFD) Close() error {
	if fd == nil || fd.sysfile == nil {
		return os.EINVAL
	}

	fd.incref()
	syscall.Shutdown(fd.sysfd, syscall.SHUT_RDWR)
	fd.closing = true
	fd.decref()
	return nil
}

func (fd *netFD) shutdown(how int) error {
	if fd == nil || fd.sysfile == nil {
		return os.EINVAL
	}
	err := syscall.Shutdown(fd.sysfd, how)
	if err != nil {
		return &OpError{"shutdown", fd.net, fd.laddr, err}
	}
	return nil
}

func (fd *netFD) CloseRead() error {
	return fd.shutdown(syscall.SHUT_RD)
}

func (fd *netFD) CloseWrite() error {
	return fd.shutdown(syscall.SHUT_WR)
}

func (fd *netFD) Read(p []byte) (n int, err error) {
	if fd == nil {
		return 0, os.EINVAL
	}
	fd.rio.Lock()
	defer fd.rio.Unlock()
	fd.incref()
	defer fd.decref()
	if fd.sysfile == nil {
		return 0, os.EINVAL
	}
	for {
		n, err = syscall.Read(fd.sysfile.Fd(), p)
		if err == syscall.EAGAIN {
			if fd.rdeadline >= 0 {
				pollserver.WaitRead(fd)
				continue
			}
			err = errTimeout
		}
		if err != nil {
			n = 0
		} else if n == 0 && err == nil && fd.sotype != syscall.SOCK_DGRAM {
			err = io.EOF
		}
		break
	}
	if err != nil && err != io.EOF {
		err = &OpError{"read", fd.net, fd.raddr, err}
	}
	return
}

func (fd *netFD) ReadFrom(p []byte) (n int, sa syscall.Sockaddr, err error) {
	if fd == nil || fd.sysfile == nil {
		return 0, nil, os.EINVAL
	}
	fd.rio.Lock()
	defer fd.rio.Unlock()
	fd.incref()
	defer fd.decref()
	for {
		n, sa, err = syscall.Recvfrom(fd.sysfd, p, 0)
		if err == syscall.EAGAIN {
			if fd.rdeadline >= 0 {
				pollserver.WaitRead(fd)
				continue
			}
			err = errTimeout
		}
		if err != nil {
			n = 0
		}
		break
	}
	if err != nil {
		err = &OpError{"read", fd.net, fd.laddr, err}
	}
	return
}

func (fd *netFD) ReadMsg(p []byte, oob []byte) (n, oobn, flags int, sa syscall.Sockaddr, err error) {
	if fd == nil || fd.sysfile == nil {
		return 0, 0, 0, nil, os.EINVAL
	}
	fd.rio.Lock()
	defer fd.rio.Unlock()
	fd.incref()
	defer fd.decref()
	for {
		n, oobn, flags, sa, err = syscall.Recvmsg(fd.sysfd, p, oob, 0)
		if err == syscall.EAGAIN {
			if fd.rdeadline >= 0 {
				pollserver.WaitRead(fd)
				continue
			}
			err = errTimeout
		}
		if err == nil && n == 0 {
			err = io.EOF
		}
		break
	}
	if err != nil && err != io.EOF {
		err = &OpError{"read", fd.net, fd.laddr, err}
		return
	}
	return
}

func (fd *netFD) Write(p []byte) (n int, err error) {
	if fd == nil {
		return 0, os.EINVAL
	}
	fd.wio.Lock()
	defer fd.wio.Unlock()
	fd.incref()
	defer fd.decref()
	if fd.sysfile == nil {
		return 0, os.EINVAL
	}
	nn := 0

	for {
		var n int
		n, err = syscall.Write(fd.sysfile.Fd(), p[nn:])
		if n > 0 {
			nn += n
		}
		if nn == len(p) {
			break
		}
		if err == syscall.EAGAIN {
			if fd.wdeadline >= 0 {
				pollserver.WaitWrite(fd)
				continue
			}
			err = errTimeout
		}
		if err != nil {
			n = 0
			break
		}
		if n == 0 {
			err = io.ErrUnexpectedEOF
			break
		}
	}
	if err != nil {
		err = &OpError{"write", fd.net, fd.raddr, err}
	}
	return nn, err
}

func (fd *netFD) WriteTo(p []byte, sa syscall.Sockaddr) (n int, err error) {
	if fd == nil || fd.sysfile == nil {
		return 0, os.EINVAL
	}
	fd.wio.Lock()
	defer fd.wio.Unlock()
	fd.incref()
	defer fd.decref()
	for {
		err = syscall.Sendto(fd.sysfd, p, 0, sa)
		if err == syscall.EAGAIN {
			if fd.wdeadline >= 0 {
				pollserver.WaitWrite(fd)
				continue
			}
			err = errTimeout
		}
		break
	}
	if err == nil {
		n = len(p)
	} else {
		err = &OpError{"write", fd.net, fd.raddr, err}
	}
	return
}

func (fd *netFD) WriteMsg(p []byte, oob []byte, sa syscall.Sockaddr) (n int, oobn int, err error) {
	if fd == nil || fd.sysfile == nil {
		return 0, 0, os.EINVAL
	}
	fd.wio.Lock()
	defer fd.wio.Unlock()
	fd.incref()
	defer fd.decref()
	for {
		err = syscall.Sendmsg(fd.sysfd, p, oob, sa, 0)
		if err == syscall.EAGAIN {
			if fd.wdeadline >= 0 {
				pollserver.WaitWrite(fd)
				continue
			}
			err = errTimeout
		}
		break
	}
	if err == nil {
		n = len(p)
		oobn = len(oob)
	} else {
		err = &OpError{"write", fd.net, fd.raddr, err}
	}
	return
}

func (fd *netFD) accept(toAddr func(syscall.Sockaddr) Addr) (nfd *netFD, err error) {
	if fd == nil || fd.sysfile == nil {
		return nil, os.EINVAL
	}

	fd.incref()
	defer fd.decref()

	// See ../syscall/exec.go for description of ForkLock.
	// It is okay to hold the lock across syscall.Accept
	// because we have put fd.sysfd into non-blocking mode.
	var s int
	var rsa syscall.Sockaddr
	for {
		if fd.closing {
			return nil, os.EINVAL
		}
		syscall.ForkLock.RLock()
		s, rsa, err = syscall.Accept(fd.sysfd)
		if err != nil {
			syscall.ForkLock.RUnlock()
			if err == syscall.EAGAIN {
				if fd.rdeadline >= 0 {
					pollserver.WaitRead(fd)
					continue
				}
				err = errTimeout
			}
			return nil, &OpError{"accept", fd.net, fd.laddr, err}
		}
		break
	}
	syscall.CloseOnExec(s)
	syscall.ForkLock.RUnlock()

	if nfd, err = newFD(s, fd.family, fd.sotype, fd.net); err != nil {
		syscall.Close(s)
		return nil, err
	}
	lsa, _ := syscall.Getsockname(nfd.sysfd)
	nfd.setAddr(toAddr(lsa), toAddr(rsa))
	return nfd, nil
}

func (fd *netFD) dup() (f *os.File, err error) {
	ns, err := syscall.Dup(fd.sysfd)
	if err != nil {
		return nil, &OpError{"dup", fd.net, fd.laddr, err}
	}

	// We want blocking mode for the new fd, hence the double negative.
	if err = syscall.SetNonblock(ns, false); err != nil {
		return nil, &OpError{"setnonblock", fd.net, fd.laddr, err}
	}

	return os.NewFile(ns, fd.sysfile.Name()), nil
}

func closesocket(s int) error {
	return syscall.Close(s)
}
