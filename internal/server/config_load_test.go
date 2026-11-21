package server

import "testing"

type fakeIdentity struct {
	name string
}

func (n *fakeIdentity) GetName() string      { return n.name }
func (n *fakeIdentity) GetShortName() string { return n.name }
func (n *fakeIdentity) GetType() string      { return "fake" }
func (n *fakeIdentity) GetFullType() string  { return "fake" }

type fakeStartNode struct {
	Node
	name       string
	configured bool
	loaded     bool
}

func (n *fakeStartNode) GetName() string { return n.name }
func (n *fakeStartNode) Start() error    { return nil }
func (n *fakeStartNode) ConfigureInterfaces() error {
	n.configured = true
	return nil
}
func (n *fakeStartNode) LoadConfig(string, int) ([]string, error) {
	n.loaded = true
	return []string{"loaded"}, nil
}

func TestStartNodeConfiguresAndLoadsConfig(t *testing.T) {
	node := &fakeStartNode{name: "R1"}
	manager := &NetemTopologyManager{path: t.TempDir()}

	messages, err := manager.startNode(node)
	if err != nil {
		t.Fatalf("startNode returned an error: %v", err)
	}
	if !node.configured {
		t.Fatal("startNode did not call ConfigureInterfaces")
	}
	if !node.loaded {
		t.Fatal("startNode did not call LoadConfig")
	}
	if len(messages) != 1 || messages[0] != "loaded" {
		t.Fatalf("unexpected messages: %#v", messages)
	}
}
