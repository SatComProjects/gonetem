package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/moby/term"
	"github.com/mroy31/gonetem/internal/proto"
	"golang.org/x/sys/unix"
)

type execSender struct {
	mu     sync.Mutex
	stream proto.Netem_NodeExecClient
}

func (s *execSender) Send(msg *proto.ExecCltMsg) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream.Send(msg)
}

func termGetTtySize(terminalFd uintptr) (int, int) {
	ws, err := term.GetWinsize(terminalFd)
	if err != nil && ws == nil {
		return 0, 0
	}
	return int(ws.Height), int(ws.Width)
}

func termResizeTty(sender *execSender, terminalFd uintptr) error {
	height, width := termGetTtySize(terminalFd)
	if height == 0 && width == 0 {
		return nil
	}
	return sender.Send(&proto.ExecCltMsg{
		Code:      proto.ExecCltMsg_RESIZE,
		TtyWidth:  int32(width),
		TtyHeight: int32(height),
	})
}

func termMonitorTty(sender *execSender, terminalFd uintptr, done <-chan struct{}) {
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGWINCH)
	signal.Notify(sigchan, syscall.SIGHUP)
	go func() {
		defer signal.Stop(sigchan)
		for {
			select {
			case <-done:
				return
			case sig := <-sigchan:
				switch sig {
				case syscall.SIGWINCH:
					_ = termResizeTty(sender, terminalFd)
				case syscall.SIGHUP:
					// terminal has been closed, send exit to
					_ = sender.Send(&proto.ExecCltMsg{
						Code: proto.ExecCltMsg_CLOSE,
					})
				}
			}
		}
	}()
}

func nodeExec(
	client proto.NetemClient,
	prjId string,
	node string,
	cmd []string,
) error {
	var (
		terminalFd uintptr
		out        io.Writer = os.Stdout
		oldState   *term.State
		err        error
	)

	if file, ok := out.(*os.File); ok {
		terminalFd = file.Fd()
	} else {
		return errors.New("not a terminal")
	}

	// Set up the pseudo terminal
	oldState, err = term.SetRawTerminal(terminalFd)
	if err != nil {
		return err
	}

	// Clean up after the command has exited
	defer term.RestoreTerminal(terminalFd, oldState)

	stream, err := client.NodeExec(context.Background())
	if err != nil {
		return err
	}
	defer stream.CloseSend()

	sender := &execSender{stream: stream}
	tHeight, tWidth := termGetTtySize(terminalFd)
	if err := sender.Send(&proto.ExecCltMsg{
		Code:      proto.ExecCltMsg_CMD,
		PrjId:     prjId,
		Node:      node,
		Cmd:       cmd,
		Tty:       true,
		TtyHeight: int32(tHeight),
		TtyWidth:  int32(tWidth),
	}); err != nil {
		return err
	}

	inputDone := make(chan error)
	outputDone := make(chan error)
	var outputClosed atomic.Bool

	// read stdin
	go func() {
		data := make([]byte, 32)
		for {
			inFd := int(os.Stdin.Fd())
			rFDSet := &unix.FdSet{}
			rFDSet.Zero()
			rFDSet.Set(inFd)

			if _, err := unix.Select(int(inFd)+1, rFDSet, nil, nil, &unix.Timeval{
				Sec:  0,
				Usec: 100000, // 100ms
			}); err != nil {
				continue
			}

			if outputClosed.Load() {
				inputDone <- nil
				return
			}

			if rFDSet.IsSet(inFd) {
				n, err := os.Stdin.Read(data)

				if err == io.EOF {
					inputDone <- nil
					return
				}

				if err != nil {
					inputDone <- err
					return
				}

				if err := sender.Send(&proto.ExecCltMsg{
					Code: proto.ExecCltMsg_DATA,
					Data: data[:n],
				}); err != nil {
					if err != io.EOF {
						inputDone <- err
					} else {
						inputDone <- nil
					}
					return
				}
			}
		}
	}()

	// receive sdtout
	go func() {
		for {
			in, err := stream.Recv()

			if err == io.EOF { // read done.
				outputDone <- nil
				return
			}

			if err != nil {
				outputDone <- err
				return
			}

			switch in.GetCode() {
			case proto.ExecSrvMsg_CLOSE:
				outputDone <- nil
				return
			case proto.ExecSrvMsg_ERROR:
				// the serveur return an error
				outputDone <- fmt.Errorf("%s", string(in.GetData()))
				return
			case proto.ExecSrvMsg_STDOUT:
				if _, err := os.Stdout.Write(in.GetData()); err != nil {
					fmt.Printf("Error os.Stdout.Write: %v\n", err)
					outputDone <- err
					return
				}
			case proto.ExecSrvMsg_STDERR:
				if _, err := os.Stderr.Write(in.GetData()); err != nil {
					fmt.Printf("Error os.Stdout.Write: %v\n", err)
					outputDone <- err
					return
				}
			}

		}
	}()

	monitorDone := make(chan struct{})
	defer close(monitorDone)
	termMonitorTty(sender, terminalFd, monitorDone)

	// wait output to finish
	err = <-outputDone
	outputClosed.Store(true)

	// wait input to finish
	<-inputDone

	return err
}
