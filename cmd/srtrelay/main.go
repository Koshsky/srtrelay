package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"srtrelay/internal/config"
	"srtrelay/internal/relay"
)

func main() {
	var (
		inputAddr       = flag.String("input-addr", ":9000", "SRT listener address for the source publisher")
		inputStreamID   = flag.String("input-streamid", "", "Required streamid for the source publisher; empty accepts any")
		inputPassphrase = flag.String("input-passphrase", "", "Passphrase required for the source publisher")
		bufferSize      = flag.Int("buffer-size", 1316*8, "Read buffer size in bytes")
		writeTimeout    = flag.Duration("write-timeout", 0, "Per-output write deadline, 0 disables deadlines")
	)

	var outputs config.OutputFlag
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

	parsedOutputs, err := config.ParseOutputs(outputs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid output configuration: %v\n", err)
		os.Exit(2)
	}

	appCfg := config.Config{
		Input: config.Endpoint{
			Addr:       *inputAddr,
			StreamID:   strings.TrimSpace(*inputStreamID),
			Passphrase: strings.TrimSpace(*inputPassphrase),
		},
		Outputs:      parsedOutputs,
		BufferSize:   *bufferSize,
		WriteTimeout: *writeTimeout,
	}

	app := relay.New(appCfg, log.New(os.Stdout, "srtrelay: ", log.LstdFlags|log.Lmicroseconds))

	if err := app.Run(); err != nil {
		log.Fatalf("relay stopped: %v", err)
	}
}
