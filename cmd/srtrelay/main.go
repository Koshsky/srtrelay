package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	gosrt "github.com/datarhei/gosrt"
)

type outputFlag []string

func (f *outputFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *outputFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("empty output specification")
	}

	*f = append(*f, value)
	return nil
}

type endpoint struct {
	addr       string
	streamID   string
	passphrase string
}

type relayOutput struct {
	endpoint endpoint
	listener gosrt.Listener

	mu    sync.RWMutex
	conns map[uint32]gosrt.Conn
}

func newRelayOutput(endpoint endpoint) *relayOutput {
	return &relayOutput{
		endpoint: endpoint,
		conns:    make(map[uint32]gosrt.Conn),
	}
}

func (o *relayOutput) addConn(conn gosrt.Conn) {
	id := conn.SocketId()

	o.mu.Lock()
	defer o.mu.Unlock()

	o.conns[id] = conn
}

func (o *relayOutput) removeConn(conn gosrt.Conn) {
	id := conn.SocketId()

	o.mu.Lock()
	defer o.mu.Unlock()

	if existing, ok := o.conns[id]; ok && existing == conn {
		delete(o.conns, id)
	}
}

func (o *relayOutput) subscriberCount() int {
	o.mu.RLock()
	defer o.mu.RUnlock()

	return len(o.conns)
}

func (o *relayOutput) write(data []byte, writeTimeout time.Duration) error {
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

func (o *relayOutput) close() {
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

var errNoSubscriber = errors.New("no subscriber connected")

func main() {
	var (
		inputAddr       = flag.String("input-addr", ":9000", "SRT listener address for the source publisher")
		inputStreamID   = flag.String("input-streamid", "", "Required streamid for the source publisher; empty accepts any")
		inputPassphrase = flag.String("input-passphrase", "", "Passphrase required for the source publisher")
		bufferSize      = flag.Int("buffer-size", 1316*8, "Read buffer size in bytes")
		writeTimeout    = flag.Duration("write-timeout", 0, "Per-output write deadline, 0 disables deadlines")
	)

	var outputs outputFlag
	flag.Var(&outputs, "output", "Output listener in the form addr[,streamid[,passphrase]]; repeat the flag for multiple outputs")
	flag.Parse()

	if len(outputs) == 0 {
		fmt.Fprintln(os.Stderr, "at least one -output is required")
		flag.Usage()
		os.Exit(2)
	}

	if *bufferSize <= 0 {
		fmt.Fprintln(os.Stderr, "-buffer-size must be positive")
		os.Exit(2)
	}

	parsedOutputs, err := parseOutputs(outputs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid output configuration: %v\n", err)
		os.Exit(2)
	}

	relay := &relay{
		input: endpoint{
			addr:       *inputAddr,
			streamID:   strings.TrimSpace(*inputStreamID),
			passphrase: strings.TrimSpace(*inputPassphrase),
		},
		outputs:      make([]*relayOutput, 0, len(parsedOutputs)),
		bufferSize:   *bufferSize,
		writeTimeout: *writeTimeout,
		logger:       log.New(os.Stdout, "srtrelay: ", log.LstdFlags|log.Lmicroseconds),
	}

	for _, out := range parsedOutputs {
		relay.outputs = append(relay.outputs, newRelayOutput(out))
	}

	if err := relay.run(); err != nil && !errors.Is(err, errSignal) {
		log.Fatalf("relay stopped: %v", err)
	}
	if errors.Is(err, errSignal) {
		relay.logger.Printf("shutdown complete")
	}
}

type relay struct {
	input        endpoint
	outputs      []*relayOutput
	bufferSize   int
	writeTimeout time.Duration
	logger       *log.Logger
}

var errSignal = errors.New("received shutdown signal")

func (r *relay) run() error {
	inputConfig := gosrt.DefaultConfig()
	inputConfig.StreamId = r.input.streamID
	inputConfig.Passphrase = r.input.passphrase

	inputListener, err := gosrt.Listen("srt", r.input.addr, inputConfig)
	if err != nil {
		return fmt.Errorf("listen on input %s: %w", r.input.addr, err)
	}
	defer inputListener.Close()

	for _, out := range r.outputs {
		outputConfig := gosrt.DefaultConfig()
		outputConfig.StreamId = out.endpoint.streamID
		outputConfig.Passphrase = out.endpoint.passphrase

		listener, err := gosrt.Listen("srt", out.endpoint.addr, outputConfig)
		if err != nil {
			return fmt.Errorf("listen on output %s: %w", out.endpoint.addr, err)
		}

		out.listener = listener
		go r.acceptOutput(out)
		streamID := out.endpoint.streamID
		if streamID == "" {
			streamID = "<any>"
		}
		r.logger.Printf("output listener ready on %s streamid=%s", out.endpoint.addr, streamID)
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

	inputStreamID := r.input.streamID
	if inputStreamID == "" {
		inputStreamID = "<any>"
	}
	r.logger.Printf("input listener ready on %s streamid=%s", r.input.addr, inputStreamID)

	for {
		conn, mode, err := inputListener.Accept(func(req gosrt.ConnRequest) gosrt.ConnType {
			r.logger.Printf("incoming publisher request from %s streamid=%q", req.RemoteAddr(), req.StreamId())
			if r.input.streamID != "" && req.StreamId() != r.input.streamID {
				r.logger.Printf("rejecting publisher from %s: streamid mismatch (got=%q want=%q)", req.RemoteAddr(), req.StreamId(), r.input.streamID)
				return gosrt.REJECT
			}

			if r.input.passphrase != "" {
				if err := req.SetPassphrase(r.input.passphrase); err != nil {
					r.logger.Printf("rejecting input publisher: passphrase error: %v", err)
					return gosrt.REJECT
				}
			}

			return gosrt.PUBLISH
		})
		if err != nil {
			if errors.Is(err, gosrt.ErrListenerClosed) {
				return errSignal
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

func (r *relay) acceptOutput(out *relayOutput) {
	for {
		conn, mode, err := out.listener.Accept(func(req gosrt.ConnRequest) gosrt.ConnType {
			if out.endpoint.streamID != "" && req.StreamId() != out.endpoint.streamID {
				return gosrt.REJECT
			}

			if out.endpoint.passphrase != "" {
				if err := req.SetPassphrase(out.endpoint.passphrase); err != nil {
					r.logger.Printf("rejecting subscriber on %s: passphrase error: %v", out.endpoint.addr, err)
					return gosrt.REJECT
				}
			}

			return gosrt.SUBSCRIBE
		})
		if err != nil {
			if errors.Is(err, gosrt.ErrListenerClosed) {
				return
			}

			r.logger.Printf("output listener %s stopped: %v", out.endpoint.addr, err)
			return
		}

		if mode != gosrt.SUBSCRIBE || conn == nil {
			continue
		}

		out.addConn(conn)

		r.logger.Printf("subscriber connected on %s from %s (active=%d)", out.endpoint.addr, conn.RemoteAddr(), out.subscriberCount())
	}
}

func (r *relay) pump(input gosrt.Conn) error {
	buf := make([]byte, r.bufferSize)

	for {
		n, err := input.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			for _, out := range r.outputs {
				writeErr := out.write(chunk, r.writeTimeout)
				if writeErr != nil && !errors.Is(writeErr, errNoSubscriber) {
					r.logger.Printf("output %s write failed: %v", out.endpoint.addr, writeErr)
				}
			}
		}

		if err != nil {
			return err
		}
	}
}

func parseOutputs(values []string) ([]endpoint, error) {
	outputs := make([]endpoint, 0, len(values))

	for _, value := range values {
		parts := strings.Split(value, ",")
		if len(parts) == 0 || len(parts) > 3 {
			return nil, fmt.Errorf("%q must be addr[,streamid[,passphrase]]", value)
		}

		addr := strings.TrimSpace(parts[0])
		if addr == "" {
			return nil, fmt.Errorf("%q has empty address", value)
		}

		endpoint := endpoint{addr: addr}
		if len(parts) > 1 {
			endpoint.streamID = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			endpoint.passphrase = strings.TrimSpace(parts[2])
		}

		outputs = append(outputs, endpoint)
	}

	return outputs, nil
}
