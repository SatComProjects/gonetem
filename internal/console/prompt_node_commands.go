package console

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/elk-language/go-prompt"
	"github.com/fatih/color"
	"github.com/google/shlex"
	"github.com/satcomprojects/gonetem/internal/proto"
	"github.com/satcomprojects/gonetem/internal/utils"
	"github.com/vbauerster/mpb/v8"
)

func (p *NetemPrompt) CopyFrom(srcNode, srcPath, destPath string) {
	file, err := os.Create(destPath)
	if err != nil {
		RedPrintf("Unable to create/open %s: %v\n", destPath, err)
		return
	}
	defer file.Close()

	client, err := p.newClient()
	if err != nil {
		RedPrintf("Unable to connect to gonetem server: %v\n", err)
		return
	}
	defer client.Close()

	stream, err := client.Client.NodeCopyFrom(context.Background(), &proto.CopyMsg{
		Code:     proto.CopyMsg_INIT,
		PrjId:    p.prjID,
		Node:     srcNode,
		NodePath: srcPath,
	})
	if err != nil {
		RedPrintf("CopyFrom %s:%s returns an error: %v\n", srcNode, srcPath, err)
		return
	}

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			RedPrintf("%v\n", err)
			return
		}

		switch msg.GetCode() {
		case proto.CopyMsg_DATA:
			if _, err := file.Write(msg.GetData()); err != nil {
				RedPrintf("Unable to write %s: %v\n", destPath, err)
				return
			}
		case proto.CopyMsg_ERROR:
			RedPrintf("CopyFrom %s:%s returns an error: %s\n", srcNode, srcPath, string(msg.GetData()))
		}
	}
}

func (p *NetemPrompt) CopyTo(srcPath, destNode, destPath string) {
	stat, err := os.Stat(srcPath)
	if os.IsNotExist(err) {
		RedPrintf("File %s does not exist\n", srcPath)
		return
	} else if err != nil {
		RedPrintf("Unable to stat %s: %v\n", srcPath, err)
		return
	}

	// check it is a regular file
	if !stat.Mode().IsRegular() {
		RedPrintf("%s is not a regular file\n", srcPath)
		return
	}

	client, err := p.newClient()
	if err != nil {
		RedPrintf("Unable to connect to gonetem server: %v\n", err)
		return
	}
	defer client.Close()

	buffer := make([]byte, 1024)
	file, err := os.Open(srcPath)
	if err != nil {
		RedPrintf("Unable to open %s: %v\n", srcPath, err)
		return
	}
	defer file.Close()

	stream, err := client.Client.NodeCopyTo(context.Background())
	if err != nil {
		RedPrintf("CopyTo: %v\n", err)
		return
	}

	if err := stream.Send(&proto.CopyMsg{
		Code:     proto.CopyMsg_INIT,
		PrjId:    p.prjID,
		Node:     destNode,
		NodePath: destPath,
	}); err != nil {
		RedPrintf("Unable to init CopyTo: %v\n", err)
		return
	}

	for {
		n, err := file.Read(buffer)
		if err != nil {
			break
		}

		if err := stream.Send(&proto.CopyMsg{
			Code: proto.CopyMsg_DATA,
			Data: buffer[:n],
		}); err != nil {
			RedPrintf("Error in CopyTo cmd: %v\n", err)
			return
		}
	}

	ack, err := stream.CloseAndRecv()
	if err != nil {
		RedPrintf("Error in CopyTo cmd: %v\n", err)
		return
	} else if ack.GetStatus().GetCode() == proto.StatusCode_ERROR {
		RedPrintf("CopyTo returns an error: %s\n", ack.Status.Error)
	}
}

func (p *NetemPrompt) Capture(cmdArgs []string) {
	args := strings.Split(cmdArgs[0], ".")
	ifIndex, _ := strconv.Atoi(args[1])

	// Check wireshark is present
	wiresharkPath, err := p.tools.LookPath("wireshark")
	if err != nil {
		RedPrintf("wireshark is not installed\n")
		return
	}

	client, err := p.newClient()
	if err != nil {
		RedPrintf("Unable to connect to gonetem server: %v", err)
		return
	}

	stream, err := client.Client.NodeCapture(context.Background(), &proto.NodeInterfaceRequest{
		PrjId:   p.prjID,
		Node:    args[0],
		IfIndex: int32(ifIndex),
	})
	if err != nil {
		RedPrintf("Error when start capturing %v\n", err)
		return
	}

	msg, err := stream.Recv()
	if err != nil {
		RedPrintf("Error when start capturing %v\n", err)
		return
	}

	if msg.GetCode() == proto.CaptureSrvMsg_ERROR {
		RedPrintf("%s\n", string(msg.GetData()))
		return
	}

	rIn, wIn := io.Pipe()

	captureArgs := []string{
		"-o", fmt.Sprintf("gui.window_title:%s@%s", args[1], args[0]),
		"-k", "-i", "-"}
	proc, err := p.tools.Start(wiresharkPath, captureArgs, rIn, nil, nil)
	if err != nil {
		RedPrintf("Error when starting wireshark: %v\n", err)
		return
	}

	go func() {
		defer client.Close()

		for {
			msg, err := stream.Recv()
			if err != nil {
				return
			}

			switch msg.GetCode() {
			case proto.CaptureSrvMsg_STDOUT:
				if _, err := wIn.Write(msg.GetData()); err != nil {
					// The capture process is gone (the pipe is closed);
					// there is nothing left to forward.
					return
				}
			case proto.CaptureSrvMsg_ERROR:
				// Best-effort termination of the capture process.
				_ = proc.Signal(os.Kill)
				return
			}
		}

	}()
}

func (p *NetemPrompt) IfState(client proto.NetemClient, cmdArgs []string) {
	state, found := map[string]proto.IfState{
		"up":   proto.IfState_UP,
		"down": proto.IfState_DOWN,
	}[cmdArgs[1]]
	if !found {
		RedPrintf("State is not valid: up|down expected\n")
		return
	}

	ifArgs := strings.Split(cmdArgs[0], ".")
	ifIndex, _ := strconv.Atoi(ifArgs[1])

	_, err := client.NodeSetIfState(
		context.Background(),
		&proto.NodeIfStateRequest{
			PrjId:   p.prjID,
			Node:    ifArgs[0],
			IfIndex: int32(ifIndex),
			State:   state,
		})
	if err != nil {
		RedPrintf("Unable to change interface state: %v\n", err)
	}
}

func (p *NetemPrompt) Check(client proto.NetemClient, cmdArgs []string) {
	ack, err := client.TopologyCheck(context.Background(), &proto.ProjectRequest{Id: p.prjID})
	if err != nil {
		RedPrintf(err.Error() + "\n")
	} else {
		if ack.Status.Code == proto.StatusCode_OK {
			fmt.Println(color.GreenString("Network is OK"))
		} else {
			RedPrintf(ack.Status.Error + "\n")
		}
	}
}

func (p *NetemPrompt) Config(client proto.NetemClient, cmdArgs []string) {
	dstPath := cmdArgs[0]
	stat, err := os.Stat(dstPath)
	if err != nil {
		RedPrintf("Unable to get stat on dest path '%s'\n\t%v\n", dstPath, err)
		return
	} else if !stat.IsDir() {
		RedPrintf("Dest path '%s' is not a directory\n", dstPath)
		return
	}

	response, err := client.ProjectGetNodeConfigs(context.Background(), &proto.ProjectRequest{Id: p.prjID})
	if err != nil {
		RedPrintf("Unable to get project configuration files: %v\n", err)
		return
	}

	buffer := bytes.NewBuffer(response.GetData())
	if err := utils.OpenArchive(dstPath, buffer); err != nil {
		RedPrintf("Unable to extract configuration files: %v\n", err)
	}
}

func (p *NetemPrompt) Exec(client proto.NetemClient, cmdArgs []string) {
	node := cmdArgs[0]

	cmd, err := shlex.Split(cmdArgs[1])
	if err != nil {
		RedPrintf(err.Error() + "\n")
		return
	}

	if err := nodeExec(client, p.prjID, node, cmd); err != nil {
		RedPrintf(err.Error() + "\n")
	}
}

func (p *NetemPrompt) startConsole(client proto.NetemClient, nodeName string, shell bool) {
	response, err := client.ProjectGetStatus(context.Background(), &proto.ProjectRequest{Id: p.prjID})
	if err != nil {
		RedPrintf("Unable to get project status: %v\n", err)
		return
	}

	// check project and node status
	if !response.Running {
		RedPrintf("Project is not running\n")
		return
	}
	for _, n := range response.GetNodes() {
		if n.Name == nodeName && !n.Running {
			RedPrintf("Node is not running\n")
			return
		}
	}

	node := fmt.Sprintf("%s.%s", p.prjID, nodeName)
	shellOpt := ""
	if shell {
		shellOpt = "--shell "
	}
	consoleCmd := struct {
		Name string
		Cmd  string
	}{Name: nodeName, Cmd: "gonetem-console console " + shellOpt + node}

	var buf bytes.Buffer
	tmpl, err := template.New("terminal").Parse(p.config.Terminal)
	if err != nil {
		RedPrintf("Unable to parse terminal line in config file: %v", err)
		return
	}
	err = tmpl.Execute(&buf, consoleCmd)
	if err != nil {
		RedPrintf("Unable to parse terminal line in config file: %v", err)
		return
	}

	args, err := shlex.Split(buf.String())
	if err != nil {
		RedPrintf("Bad command line: %v", err)
		return
	}

	// search term command
	termPath, err := p.tools.LookPath(args[0])
	if err != nil {
		RedPrintf("terminal '%s' is not installed\n", args[0])
		return
	}

	proc, err := p.tools.Start(termPath, args[1:], nil, nil, nil)
	if err != nil {
		RedPrintf("Error when starting console: %v\n", err)
		return
	}

	p.processes = append(p.processes, proc)
}

func (p *NetemPrompt) startConsoleAll(client proto.NetemClient, shell bool) {
	response, err := client.ProjectGetStatus(context.Background(), &proto.ProjectRequest{Id: p.prjID})
	if err != nil {
		RedPrintf("Unable to get project status: %v\n", err)
		return
	}

	for _, node := range response.GetNodes() {
		if node.Running {
			p.startConsole(client, node.GetName(), shell)
		}
	}
}

func (p *NetemPrompt) Run(client proto.NetemClient, cmdArgs []string) {
	stream, err := client.TopologyRun(context.Background(), &proto.ProjectRequest{Id: p.prjID})
	if err != nil {
		RedPrintf("Unable to run the project: %v\n", err)
		return
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
			RedPrintf("Unable to run topology: %v\n", err)
			return
		}

		ProgressRunHandleMsg(mpBar, bars, msg)
	}

	mpBar.Wait()
}

func (p *NetemPrompt) ViewNodeConfiguration(client proto.NetemClient, cmdArgs []string) {
	response, err := client.NodeReadConfigFiles(context.Background(), &proto.NodeRequest{PrjId: p.prjID, Node: cmdArgs[0]})
	if err != nil {
		RedPrintf("Unable to to read configuration files: %v\n", err)
		return
	}

	if len(response.Files) == 1 {
		configFile := response.Files[0]
		fmt.Println("#####  " + configFile.Name + "  #####")
		fmt.Printf("\n%s\n", configFile.Data)
		return
	}

	configName := prompt.Input(
		prompt.WithPrefix("Select configuration file: "),
		prompt.WithCompleter(NewViewConfigCompleter(response).Complete),
		prompt.WithShowCompletionAtStart(),
	)
	configName = strings.TrimSpace(configName)

	for _, configFile := range response.Files {
		if configFile.Name == configName {
			fmt.Println("#####  " + configFile.Name + "  #####")
			fmt.Printf("\n%s\n", configFile.Data)
			return
		}
	}
	fmt.Printf(color.YellowString("Configuration file %s not found\n"), configName)
}
