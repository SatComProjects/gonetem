package console

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"time"

	"github.com/briandowns/spinner"
	"github.com/fatih/color"
	"github.com/satcomprojects/gonetem/internal/proto"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

func (p *NetemPrompt) save(client proto.NetemClient, dstPath string) {
	stream, err := client.ProjectSave(context.Background(), &proto.ProjectRequest{Id: p.prjID})
	if err != nil {
		RedPrintf("Unable to save project: %v\n", err)
		return
	}

	mpBar := mpb.New(mpb.WithWidth(48))
	bars := make([]ProgressBarT, 1)

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			ProgressForceComplete(bars)
			break
		} else if err != nil {
			ProgressAbort(bars, true)
			RedPrintf("Unable to save project: %v\n", err)
			return
		}

		switch msg.Code {
		case proto.ProjectSaveMsg_NODE_COUNT:
			if msg.GetTotal() > 0 {
				bars[0] = ProgressBarT{
					Total: int(msg.Total),
					Bar: mpBar.AddBar(int64(msg.Total),
						mpb.BarRemoveOnComplete(),
						mpb.PrependDecorators(decor.Counters(0, "Save nodes: %d/%d")),
					),
				}
			}

		case proto.ProjectSaveMsg_NODE_SAVE:
			bars[0].Bar.Increment()

		case proto.ProjectSaveMsg_DATA:
			ProgressForceComplete(bars)
			if err := os.WriteFile(dstPath, msg.GetData(), 0644); err != nil {
				RedPrintf("Unable to write saved project to %s: %v\n", dstPath, err)
			}
		}
	}

	mpBar.Wait()
}

func (p *NetemPrompt) Save(client proto.NetemClient, cmdArgs []string) {
	if p.prjPath == "" {
		RedPrintf("Project path is empty, use saveAs command if you connect to running project\n")
		return
	}

	p.save(client, p.prjPath)
}

func (p *NetemPrompt) SaveAs(client proto.NetemClient, cmdArgs []string) {
	p.save(client, cmdArgs[0])
}

func (p *NetemPrompt) Start(client proto.NetemClient, cmdArgs []string) {
	ack, err := client.NodeStart(p.getCancelContext(), &proto.NodeRequest{PrjId: p.prjID, Node: cmdArgs[0]})
	if err != nil {
		RedPrintf("Unable to start node: %v\n", err)
	} else if ack.Status.Code == proto.StatusCode_ERROR {
		MagentaPrintf(ack.Status.Error + "\n")
	}
}

func (p *NetemPrompt) StartAll(client proto.NetemClient, cmdArgs []string) {
	ack, err := client.TopologyStartAll(context.Background(), &proto.ProjectRequest{Id: p.prjID})
	if err != nil {
		RedPrintf("Unable to start all nodes: %v\n", err)
	} else if ack.Status.Code == proto.StatusCode_ERROR {
		MagentaPrintf(ack.Status.Error + "\n")
	}
}

func (p *NetemPrompt) Stop(client proto.NetemClient, cmdArgs []string) {
	ack, err := client.NodeStop(context.Background(), &proto.NodeRequest{PrjId: p.prjID, Node: cmdArgs[0]})
	if err != nil {
		RedPrintf("Unable to stop node: %v\n", err)
	} else if ack.Status.Code == proto.StatusCode_ERROR {
		MagentaPrintf(ack.Status.Error + "\n")
	}
}

func (p *NetemPrompt) StopAll(client proto.NetemClient, cmdArgs []string) {
	ack, err := client.TopologyStopAll(context.Background(), &proto.ProjectRequest{Id: p.prjID})
	if err != nil {
		RedPrintf("Unable to stop all nodes: %v\n", err)
	} else if ack.Status.Code == proto.StatusCode_ERROR {
		MagentaPrintf(ack.Status.Error + "\n")
	}
}

func (p *NetemPrompt) Restart(client proto.NetemClient, cmdArgs []string) {
	ack, err := client.NodeRestart(context.Background(), &proto.NodeRequest{PrjId: p.prjID, Node: cmdArgs[0]})
	if err != nil {
		RedPrintf("Unable to restart node: %v\n", err)
	} else if ack.Status.Code == proto.StatusCode_ERROR {
		MagentaPrintf(ack.Status.Error + "\n")
	}
}

func (p *NetemPrompt) Status(client proto.NetemClient, cmdArgs []string) {
	response, err := client.ProjectGetStatus(context.Background(), &proto.ProjectRequest{Id: p.prjID})
	if err != nil {
		RedPrintf("Unable to get project status: %v\n", err)
		return
	}

	fmt.Println("Project " + response.GetName())
	fmt.Println("- Id: " + response.GetId())
	fmt.Println("- OpenAt: " + response.GetOpenAt())
	fmt.Print("- State: ")
	if response.GetRunning() {
		fmt.Print(color.GreenString("Running\n"))
	} else {
		fmt.Print(color.YellowString("Not Running\n"))
	}

	if len(response.GetNodes()) > 0 {
		fmt.Println("- Nodes:")
		for _, nodeInfo := range response.GetNodes() {
			fmt.Print("   - " + nodeInfo.GetName() + ": ")
			if nodeInfo.GetRunning() {
				fmt.Print(color.GreenString("Running\n"))
				for _, i := range nodeInfo.GetInterfaces() {
					fmt.Print("     - " + i.GetName() + ": ")
					switch i.GetState() {
					case proto.IfState_DOWN:
						fmt.Print(color.MagentaString("Down\n"))
					case proto.IfState_UP:
						fmt.Print(color.GreenString("Up\n"))
					}
				}
			} else {
				fmt.Print(color.MagentaString("Not Running\n"))
			}
		}
	}
}

func (p *NetemPrompt) Edit(client proto.NetemClient, cmdArgs []string) {
	// first, check editor exists
	if _, err := p.tools.LookPath(p.config.Editor); err != nil {
		RedPrintf("Editor set in config, %s, is not found\n", p.config.Editor)
		return
	}

	response, err := client.ReadNetworkFile(context.Background(), &proto.ProjectRequest{Id: p.prjID})
	if err != nil {
		RedPrintf("Unable to get network file: %v\n", err)
		return
	}
	// write temp file for edition
	tempFilename := path.Join("/tmp", "gonetem-network-"+p.prjID)
	if err := os.WriteFile(tempFilename, response.GetData(), 0644); err != nil {
		RedPrintf("Unable to write temp file for edition: %v\n", err)
		return
	}
	defer os.Remove(tempFilename)

	if err := editFileWithRunner(tempFilename, p.config.Editor, p.tools); err != nil {
		RedPrintf("Unable to edit temp file: %v\n", err)
		return
	}

	data, err := os.ReadFile(tempFilename)
	if err != nil {
		RedPrintf("Unable to read edited network file: %v\n", err)
		return
	}

	if _, err = client.WriteNetworkFile(context.Background(), &proto.WNetworkRequest{
		Id:   p.prjID,
		Data: data,
	}); err != nil {
		Fatal("Unable to write modified network file on server: %v\n", err)
	}
}

func (p *NetemPrompt) Reload(client proto.NetemClient, cmdArgs []string) {
	s := spinner.New(spinner.CharSets[9], 100*time.Millisecond)
	s.Prefix = "Reload project " + p.prjPath + " : "
	s.Start()

	stream, err := client.TopologyReload(context.Background(), &proto.ProjectRequest{Id: p.prjID})
	if err != nil {
		s.Stop()
		RedPrintf("Unable to reload the project: %v\n", err)
		return
	}

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		} else if err != nil {
			s.Stop()
			RedPrintf("Unable to run topology: %v\n", err)
			break
		}

		if msg.Code == proto.TopologyRunMsg_NODE_MESSAGES {
			s.Stop()
			for _, nMessages := range msg.NodeMessages {
				if len(nMessages.Messages) > 0 {
					fmt.Println(color.YellowString(nMessages.Name + ":"))
					for _, msg := range nMessages.Messages {
						if msg != "" {
							fmt.Println(color.YellowString("  - " + msg))
						}
					}
				}
			}
		}
	}
}
