package dtls

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	coapNet "github.com/plgd-dev/go-coap/v2/net"
	"github.com/plgd-dev/go-coap/v2/udp/client"
	"github.com/plgd-dev/go-coap/v2/udp/message/pool"
)

type EventFunc = func()

type Session struct {
	connection     *coapNet.Conn
	maxMessageSize int
	closeSocket    bool

	mutex   sync.Mutex
	onClose []EventFunc

	cancel context.CancelFunc
	ctx    atomic.Value
}

func NewSession(
	ctx context.Context,
	connection *coapNet.Conn,
	maxMessageSize int,
	closeSocket bool,
) *Session {
	ctx, cancel := context.WithCancel(ctx)
	s := &Session{
		cancel:         cancel,
		connection:     connection,
		maxMessageSize: maxMessageSize,
		closeSocket:    closeSocket,
	}
	s.ctx.Store(&ctx)
	return s
}

func (s *Session) Done() <-chan struct{} {
	return s.Context().Done()
}

func (s *Session) AddOnClose(f EventFunc) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.onClose = append(s.onClose, f)
}

func (s *Session) popOnClose() []EventFunc {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	tmp := s.onClose
	s.onClose = nil
	return tmp
}

func (s *Session) close() error {
	for _, f := range s.popOnClose() {
		f()
	}
	if s.closeSocket {
		return s.connection.Close()
	}
	return nil
}

func (s *Session) Close() error {
	s.cancel()
	return nil
}

func (s *Session) Context() context.Context {
	return *s.ctx.Load().(*context.Context)
}

// SetContextValue stores the value associated with key to context of connection.
func (s *Session) SetContextValue(key interface{}, val interface{}) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	ctx := context.WithValue(s.Context(), key, val)
	s.ctx.Store(&ctx)
}

func (s *Session) WriteMessage(req *pool.Message) error {
	data, err := req.Marshal()
	if err != nil {
		return fmt.Errorf("cannot marshal: %w", err)
	}
	return s.connection.WriteWithContext(req.Context(), data)
}

func (s *Session) MaxMessageSize() int {
	return s.maxMessageSize
}

func (s *Session) RemoteAddr() net.Addr {
	return s.connection.RemoteAddr()
}

// Run reads and process requests from a connection, until the connection is not closed.
func (s *Session) Run(cc *client.ClientConn) (err error) {
	defer func() {
		err1 := s.Close()
		if err == nil {
			err = err1
		}
		err1 = s.close()
		if err == nil {
			err = err1
		}
	}()
	m := make([]byte, s.maxMessageSize)

	timeout := time.Second * 300
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	go func() {
		for {
			select {
			case <-timer.C:
				timer.Stop()
				s.Close()
				return
			}
		}
	}()

	for {
		readBuf := m
		readLen, err := s.connection.ReadWithContext(s.Context(), readBuf)
		timer.Reset(timeout)

		if err != nil {
			return err
		}
		readBuf = readBuf[:readLen]
		err = cc.Process(readBuf)
		if err != nil {
			return err
		}
	}
}
