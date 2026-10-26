package console

import "testing"

func TestExecuteIgnoresCommentOnlyInput(t *testing.T) {
	prompt := &NetemPrompt{commands: make(map[string]*NetemCommand)}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Execute panicked on comment-only input: %v", r)
		}
	}()

	prompt.Execute("# comment")
}
