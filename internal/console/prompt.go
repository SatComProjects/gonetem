package console

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/docker/docker/pkg/system"
	"github.com/fatih/color"
	"github.com/google/shlex"
	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/satcomprojects/gonetem/internal/proto"
	"github.com/vbauerster/mpb/v8"
)

type copyDirection int

const (
	fromNode copyDirection = 1 << iota
	toNode
	acrossNodes = fromNode | toNode
)

var (
	RedPrintf     = color.New(color.FgRed).PrintfFunc()
	MagentaPrintf = color.New(color.FgMagenta).PrintfFunc()
)

func Fatal(msg string, a ...interface{}) {
	RedPrintf(msg+"\n", a...)
	os.Exit(1)
}

func splitCopyArg(arg string) (container, path string) {
	if system.IsAbs(arg) {
		return "", arg
	}

	parts := strings.SplitN(arg, ":", 2)

	if len(parts) == 1 || strings.HasPrefix(parts[0], ".") {
		// Either there's no `:` in the arg
		// OR it's an explicit local relative path like `./file:name.txt`.
		return "", arg
	}

	return parts[0], parts[1]
}

type NetemNode struct {
	Name       string
	Interfaces []string
}

type NetemPrompt struct {
	server    string
	prjID     string
	prjPath   string
	processes []ToolProcess
	commands  CommandRegistry
	nodes     []NetemNode // use by completion
	tools     ToolRunner
	config    options.NetemConsoleConfig
	connect   func(server string) (*NetemConsoleClient, error)
}

func (p *NetemPrompt) Execute(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}

	if s == "quit" || s == "exit" {
		err := p.Close()
		if err != nil {
			RedPrintf(err.Error() + "\n")
		}

		os.Exit(0)
	}

	if s == "help" {
		for _, name := range p.commands.Names() {
			cmd, _ := p.commands.Lookup(name)
			fmt.Println(color.BlueString(name))
			fmt.Println("  " + cmd.Desc)
			fmt.Println("  Usage: " + cmd.Usage)
		}
		return
	}

	args, err := shlex.Split(s)
	if err != nil {
		RedPrintf("Bad command line: %v", err)
		return
	}
	if len(args) == 0 {
		return
	}

	cmd, cmdArgs, err := p.commands.Parse(args)
	if err != nil {
		RedPrintf("%v\n", err)
		return
	}

	// run the command
	cmd.Run(p, cmdArgs)
}

func (p *NetemPrompt) newClient() (*NetemConsoleClient, error) {
	if p.connect != nil {
		return p.connect(p.server)
	}
	return NewClient(p.server, p.config.Tls)
}

func (p *NetemPrompt) refreshNodeList() {
	client, err := p.newClient()
	if err != nil {
		return
	}
	defer client.Close()

	status, err := client.Client.ProjectGetStatus(
		context.Background(), &proto.ProjectRequest{Id: p.prjID})
	if err != nil {
		return
	}

	p.nodes = make([]NetemNode, 0)
	for _, node := range status.GetNodes() {
		nStatus := NetemNode{
			Name:       node.GetName(),
			Interfaces: make([]string, 0),
		}
		for _, ifStatus := range node.GetInterfaces() {
			nStatus.Interfaces = append(nStatus.Interfaces, ifStatus.Name)
		}

		p.nodes = append(p.nodes, nStatus)
	}
}

func (p *NetemPrompt) execWithClient(cmdArgs []string, execFunc func(client proto.NetemClient, cmdArgs []string)) {
	client, err := p.newClient()
	if err != nil {
		RedPrintf("Unable to connect to gonetem server: %v", err)
		return
	}
	defer client.Close()

	execFunc(client.Client, cmdArgs)
}

func (p *NetemPrompt) getCancelContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)

	signal.Notify(c, os.Interrupt)

	go func() {
		select {
		case <-c:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	return ctx
}

func (p *NetemPrompt) Close() error {
	// First, stop all running processes (console, capture...)
	for _, cmd := range p.processes {
		done := make(chan interface{})
		go func() {
			done <- cmd.Wait()
		}()
		select {
		case <-time.After(100 * time.Millisecond):
			// Best-effort kill after the grace period.
			_ = cmd.Kill()
		case <-done:
		}
	}

	client, err := p.newClient()
	if err != nil {
		return fmt.Errorf("unable to connect to server: %v", err)
	} else {
		defer client.Close()

		stream, err := client.Client.ProjectClose(context.Background(), &proto.ProjectRequest{Id: p.prjID})
		if err != nil {
			return fmt.Errorf("unable to close project: %v", err)
		}

		mpBar := mpb.New(mpb.WithWidth(48))
		bars := make([]ProgressBarT, 4)

		for {
			msg, err := stream.Recv()
			if err == io.EOF {
				ProgressForceComplete(bars)
				break
			} else if err != nil {
				ProgressAbort(bars, true)
				return err
			}

			ProgressCloseHandleMsg(mpBar, bars, msg)
		}

		mpBar.Wait()
	}

	return nil
}

func NewNetemPrompt(server, prjID, prjPath string, config options.NetemConsoleConfig) *NetemPrompt {
	p := &NetemPrompt{
		server:    server,
		prjID:     prjID,
		prjPath:   prjPath,
		processes: make([]ToolProcess, 0),
		commands:  NewCommandRegistry(),
		nodes:     make([]NetemNode, 0),
		tools:     execToolRunner{},
		config:    config,
	}
	p.RegisterCommands()
	p.refreshNodeList()

	return p
}
