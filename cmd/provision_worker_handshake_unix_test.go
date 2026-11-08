//go:build !windows

package cmd

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/provision"
)

func TestWorkerBadHandshakeHelper(t *testing.T) {
	if os.Getenv("WORKER_HANDSHAKE_FAILURE_HELPER") != "1" {
		return
	}
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	rpcReader, rpcProducer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rpcReader.Close() }()
	_ = rpcProducer.Close()
	rpcConsumer, rpcWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rpcWriter.Close() }()
	_ = rpcConsumer.Close()
	handshake, nonceWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handshake.Close() }()
	_ = nonceWriter.Close()
	previous := os.Args
	duplicate := func(file *os.File) string {
		fd, err := syscall.Dup(int(file.Fd()))
		if err != nil {
			t.Fatal(err)
		}
		return strconv.Itoa(fd)
	}
	readFD, writeFD, handshakeFD := duplicate(rpcReader), duplicate(rpcWriter), duplicate(handshake)
	os.Args = []string{previous[0], "--internal-provision-worker", readFD, writeFD, readFD, readFD, handshakeFD}
	defer func() { os.Args = previous }()
	err = runProvisionWorker()
	if !errors.Is(err, provision.ErrProtocol) || !errors.Is(err, syscall.EBADF) {
		t.Fatalf("bad handshake lost its original or aliased-descriptor cleanup error: %v", err)
	}
	if _, err := os.Stat(env.GetStorePath()); !os.IsNotExist(err) {
		t.Fatalf("invalid worker handshake materialized store content: %v", err)
	}
}

func TestWorkerBadHandshakePreservesProtocolAndCleanupErrors(t *testing.T) {
	args := []string{"-test.run=^TestWorkerBadHandshakeHelper$"}
	if directory := flag.Lookup("test.gocoverdir"); directory != nil && directory.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+directory.Value.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], args...)
	command.Env = append(os.Environ(), "WORKER_HANDSHAKE_FAILURE_HELPER=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("isolated bad handshake fixture: %v\n%s", err, output)
	}
}
