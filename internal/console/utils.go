package console

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/fatih/color"
)

func CmdErrLogger(pipe io.ReadCloser) {
	reader := bufio.NewReader(pipe)
	for {
		output, err := reader.ReadString('\n')

		if err != nil {
			fmt.Println(color.RedString("%v", err))
			return
		}

		fmt.Print(color.YellowString(output))
	}
}

func EditFile(filename, editor string) error {
	return editFileWithRunner(filename, editor, execToolRunner{})
}

func editFileWithRunner(filename, editor string, tools ToolRunner) error {
	var stdin io.Reader
	var stdout io.Writer
	if editor == "vi" || editor == "vim" || editor == "nano" {
		stdin = os.Stdin
		stdout = os.Stdout
	}

	proc, err := tools.Start(editor, []string{filename}, stdin, stdout, nil)
	if err != nil {
		return err
	}
	return proc.Wait()
}
