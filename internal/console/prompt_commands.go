package console

import "github.com/satcomprojects/gonetem/internal/proto"

func (p *NetemPrompt) RegisterCommands() {
	p.commands["capture"] = &NetemCommand{
		Desc:  "Capture traffic on an interface",
		Usage: "capture <node_name>.<if_number>",
		Args:  []string{`^\w+\.\d+$`},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.Capture(cmdArgs)
		},
	}
	p.commands["check"] = &NetemCommand{
		Desc:  "Check that the topology file is correct. If not, return found errors",
		Usage: "check",
		Args:  []string{},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.execWithClient(cmdArgs, p.Check)
		},
	}
	p.commands["config"] = &NetemCommand{
		Desc:  "Save the configuration files in the specified folder",
		Usage: "config <dest_path>",
		Args:  []string{`^.+$`},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.execWithClient(cmdArgs, p.Config)
		},
	}
	p.commands["console"] = &NetemCommand{
		Desc:  "Open a console for a node",
		Usage: "console <node_name>",
		Args:  []string{`^\w+$`},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.execWithClient(cmdArgs, func(client proto.NetemClient, cmdArgs []string) {
				if cmdArgs[0] == "all" {
					p.startConsoleAll(client, false)
				} else {
					p.startConsole(client, cmdArgs[0], false)
				}
			})
		},
	}
	p.commands["copy"] = &NetemCommand{
		Desc:  "Copy a file from/to a node",
		Usage: "copy sourceFile <node>:destFile",
		Args:  []string{`^.+$`, `^.+$`},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			srcNode, srcPath := splitCopyArg(cmdArgs[0])
			destNode, destPath := splitCopyArg(cmdArgs[1])

			var direction copyDirection
			if srcNode != "" {
				direction |= fromNode
			}
			if destNode != "" {
				direction |= toNode
			}

			switch direction {
			case fromNode:
				p.CopyFrom(srcNode, srcPath, destPath)
			case toNode:
				p.CopyTo(srcPath, destNode, destPath)
			case acrossNodes:
				RedPrintf("copying between containers is not supported\n")
			default:
				RedPrintf("must specify at least one container source\n")
			}
		},
	}
	p.commands["edit"] = &NetemCommand{
		Desc:  "Edit the topology",
		Usage: "edit",
		Args:  []string{},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.execWithClient(cmdArgs, p.Edit)
		},
	}
	p.commands["exec"] = &NetemCommand{
		Desc:  "Exec a command on a node",
		Usage: "exec <node_name> <cmd>",
		Args:  []string{`^\w+$`, `^.+$`},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.execWithClient(cmdArgs, p.Exec)
		},
	}
	p.commands["ifState"] = &NetemCommand{
		Desc:  "Enable/disable a node interface",
		Usage: "ifState <node_name>.<if_number> up|down",
		Args:  []string{`^\w+\.\d+$`, `^up|down$`},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.execWithClient(cmdArgs, p.IfState)
		},
	}
	p.commands["reload"] = &NetemCommand{
		Desc:  "Reload the project",
		Usage: "reload",
		Args:  []string{},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.execWithClient(cmdArgs, p.Reload)
			// node list needs to be updated for completion
			p.refreshNodeList()
		},
	}
	p.commands["restart"] = &NetemCommand{
		Desc:  "Restart a node",
		Usage: "restart <node_name>",
		Args:  []string{`^\w+$`},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.execWithClient(cmdArgs, p.Restart)
		},
	}
	p.commands["run"] = &NetemCommand{
		Desc:  "Start the project",
		Usage: "run",
		Args:  []string{},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.execWithClient(cmdArgs, p.Run)
			// node list needs to be updated for completion
			p.refreshNodeList()
		},
	}
	p.commands["save"] = &NetemCommand{
		Desc:  "Save the project",
		Usage: "save",
		Args:  []string{},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.execWithClient(cmdArgs, p.Save)
		},
	}
	p.commands["saveAs"] = &NetemCommand{
		Desc:  "Save the project in a new file",
		Usage: "saveAs <project_path>/<name>.gnet",
		Args:  []string{`^.*\.gnet$`},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.execWithClient(cmdArgs, p.SaveAs)
		},
	}
	p.commands["shell"] = &NetemCommand{
		Desc:  "Open a shell console for a node",
		Usage: "shell <node_name>",
		Args:  []string{`^\w+$`},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.execWithClient(cmdArgs, func(client proto.NetemClient, cmdArgs []string) {
				if cmdArgs[0] == "all" {
					p.startConsoleAll(client, true)
				} else {
					p.startConsole(client, cmdArgs[0], true)
				}
			})
		},
	}
	p.commands["start"] = &NetemCommand{
		Desc:  "Start a node",
		Usage: "start <node_name>",
		Args:  []string{`^\w+$`},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			if cmdArgs[0] == "all" {
				p.execWithClient(cmdArgs, p.StartAll)
			} else {
				p.execWithClient(cmdArgs, p.Start)
			}
		},
	}
	p.commands["stop"] = &NetemCommand{
		Desc:  "Stop a node",
		Usage: "stop <node_name>",
		Args:  []string{`^\w+$`},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			if cmdArgs[0] == "all" {
				p.execWithClient(cmdArgs, p.StopAll)
			} else {
				p.execWithClient(cmdArgs, p.Stop)
			}
		},
	}
	p.commands["status"] = &NetemCommand{
		Desc:  "Display the state of the project",
		Usage: "status",
		Args:  []string{},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.execWithClient(cmdArgs, p.Status)
		},
	}
	p.commands["viewConfig"] = &NetemCommand{
		Desc:  "Display the current configuration of a node",
		Usage: "viewConfig <node_name>",
		Args:  []string{`^\w+$`},
		Run: func(p *NetemPrompt, cmdArgs []string) {
			p.execWithClient(cmdArgs, p.ViewNodeConfiguration)
		},
	}
}
