package relay

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	cfg "srtrelay/internal/config"

	gosrt "github.com/datarhei/gosrt"
)

type outputState struct {
	endpoint cfg.Endpoint
	listener gosrt.Listener

	mu    sync.RWMutex
	conns map[uint32]gosrt.Conn
}

func newOutputState(endpoint cfg.Endpoint) *outputState {
	return &outputState{
		endpoint: endpoint,
		conns:    make(map[uint32]gosrt.Conn),
	}
}

func (o *outputState) addConn(conn gosrt.Conn) {
	id := conn.SocketId()

	o.mu.Lock()
	defer o.mu.Unlock()

	o.conns[id] = conn
}

func (o *outputState) removeConn(conn gosrt.Conn) {
	id := conn.SocketId()

	o.mu.Lock()
	defer o.mu.Unlock()

	if existing, ok := o.conns[id]; ok && existing == conn {
		delete(o.conns, id)
	}
}

func (o *outputState) subscriberCount() int {
	o.mu.RLock()
	defer o.mu.RUnlock()

	return len(o.conns)
}

func (o *outputState) write(data []byte, writeTimeout time.Duration) error {
	o.mu.RLock()
	if len(o.conns) == 0 {
		o.mu.RUnlock()
		return errNoSubscriber
	}

	conns := make([]gosrt.Conn, 0, len(o.conns))
	for _, conn := range o.conns {
		conns = append(conns, conn)
	}
	o.mu.RUnlock()

	var firstErr error
	for _, conn := range conns {
		if writeTimeout > 0 {
			_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		}

		_, err := conn.Write(data)
		if err != nil {
			o.removeConn(conn)
			_ = conn.Close()
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

func (o *outputState) close() {
	o.mu.Lock()
	conns := make([]gosrt.Conn, 0, len(o.conns))
	for _, conn := range o.conns {
		conns = append(conns, conn)
	}
	o.conns = make(map[uint32]gosrt.Conn)
	o.mu.Unlock()

	for _, conn := range conns {
		_ = conn.Close()
	}

	if o.listener != nil {
		o.listener.Close()
	}
}

var (
	errNoSubscriber = errors.New("no subscriber connected")
)

type Relay struct {
	cfg     cfg.Config
	outputs []*outputState
	logger  *log.Logger
}

func New(c cfg.Config, logger *log.Logger) *Relay {
	outputs := make([]*outputState, 0, len(c.Outputs))
	for _, out := range c.Outputs {
		outputs = append(outputs, newOutputState(out))
	}

	return &Relay{
		cfg:     c,
		outputs: outputs,
		logger:  logger,
	}
}

func (r *Relay) Run() error {
	inputConfig := gosrt.DefaultConfig()
	inputConfig.StreamId = r.cfg.Input.StreamID
	inputConfig.Passphrase = r.cfg.Input.Passphrase

	inputListener, err := gosrt.Listen("srt", r.cfg.Input.Addr, inputConfig)
	if err != nil {
		return fmt.Errorf("listen on input %s: %w", r.cfg.Input.Addr, err)
	}
	defer inputListener.Close()

	for _, out := range r.outputs {
		outputConfig := gosrt.DefaultConfig()
		outputConfig.StreamId = out.endpoint.StreamID
		outputConfig.Passphrase = out.endpoint.Passphrase

		listener, err := gosrt.Listen("srt", out.endpoint.Addr, outputConfig)
		if err != nil {
			return fmt.Errorf("listen on output %s: %w", out.endpoint.Addr, err)
		}

		out.listener = listener
		go r.acceptOutput(out)
		streamID := out.endpoint.StreamID
		if streamID == "" {
			streamID = "<any>"
		}
		r.logger.Printf("output listener ready on %s streamid=%s", out.endpoint.Addr, streamID)
	}
	defer func() {
		for _, out := range r.outputs {
			out.close()
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		<-sigCh
		inputListener.Close()
		for _, out := range r.outputs {
			out.close()
		}
	}()

	inputStreamID := r.cfg.Input.StreamID
	if inputStreamID == "" {
		inputStreamID = "<any>"
	}
	r.logger.Printf("input listener ready on %s streamid=%s", r.cfg.Input.Addr, inputStreamID)

	for {
		conn, mode, err := inputListener.Accept(func(req gosrt.ConnRequest) gosrt.ConnType {
			r.logger.Printf("incoming publisher request from %s streamid=%q", req.RemoteAddr(), req.StreamId())
			if r.cfg.Input.StreamID != "" && req.StreamId() != r.cfg.Input.StreamID {
				r.logger.Printf("rejecting publisher from %s: streamid mismatch (got=%q want=%q)", req.RemoteAddr(), req.StreamId(), r.cfg.Input.StreamID)
				return gosrt.REJECT
			}

			if r.cfg.Input.Passphrase != "" {
				if err := req.SetPassphrase(r.cfg.Input.Passphrase); err != nil {
					r.logger.Printf("rejecting input publisher: passphrase error: %v", err)
					return gosrt.REJECT
				}
			}

			return gosrt.PUBLISH
		})
		if err != nil {
			if errors.Is(err, gosrt.ErrListenerClosed) {
				return nil
			}
			return fmt.Errorf("accept input publisher: %w", err)
		}

		if mode != gosrt.PUBLISH || conn == nil {
			continue
		}

		r.logger.Printf("input publisher connected from %s", conn.RemoteAddr())
		pumpErr := r.pump(conn)
		_ = conn.Close()

		if pumpErr != nil {
			r.logger.Printf("input publisher disconnected: %v", pumpErr)
		} else {
			r.logger.Printf("input publisher disconnected")
		}
	}
}

func (r *Relay) acceptOutput(out *outputState) {
	for {
		conn, mode, err := out.listener.Accept(func(req gosrt.ConnRequest) gosrt.ConnType {
			if out.endpoint.StreamID != "" && req.StreamId() != out.endpoint.StreamID {
				return gosrt.REJECT
			}

			if out.endpoint.Passphrase != "" {
				if err := req.SetPassphrase(out.endpoint.Passphrase); err != nil {
					r.logger.Printf("rejecting subscriber on %s: passphrase error: %v", out.endpoint.Addr, err)
					return gosrt.REJECT
				}
			}

			return gosrt.SUBSCRIBE
		})
		if err != nil {
			if errors.Is(err, gosrt.ErrListenerClosed) {
				return
			}

			r.logger.Printf("output listener %s stopped: %v", out.endpoint.Addr, err)
			return
		}

		if mode != gosrt.SUBSCRIBE || conn == nil {
			continue
		}

		out.addConn(conn)
		r.logger.Printf("subscriber connected on %s from %s (active=%d)", out.endpoint.Addr, conn.RemoteAddr(), out.subscriberCount())
	}
}

func (r *Relay) pump(input gosrt.Conn) error {
	buf := make([]byte, r.cfg.BufferSize)

	for {
		n, err := input.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			for _, out := range r.outputs {
				writeErr := out.write(chunk, r.cfg.WriteTimeout)
				if writeErr != nil && !errors.Is(writeErr, errNoSubscriber) {
					r.logger.Printf("output %s write failed: %v", out.endpoint.Addr, writeErr)
				}
			}
		}

		if err != nil {
			return err
		}
	}
}
