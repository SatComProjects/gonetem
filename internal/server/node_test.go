package server

import (
	"sync"
	"testing"

	"github.com/mroy31/gonetem/internal/utils"
)

func TestCreateNamespaceNode(t *testing.T) {
	node, err := CreateNode(utils.RandString(8), "ns1", "n1", NodeConfig{Type: "namespace"})
	if err != nil {
		t.Fatalf("CreateNode returned an error: %v", err)
	}
	defer node.Close()

	if node.GetType() != "namespace" {
		t.Fatalf("node type = %q", node.GetType())
	}
	if node.GetFullType() != "namespace" {
		t.Fatalf("node full type = %q", node.GetFullType())
	}
	if node.GetName() != "ns1" || node.GetShortName() != "n1" {
		t.Fatalf("unexpected names: %q / %q", node.GetName(), node.GetShortName())
	}
}

func TestIdGenerator_ShortName(t *testing.T) {
	idGenerator := NodeIdentifierGenerator{lock: &sync.Mutex{}}
	fixedName := "nnn"

	name1 := fixedName + utils.RandString(6) + fixedName
	shortName1, _ := idGenerator.GetId(name1)
	if shortName1 != "0nnnn" {
		t.Fatalf("value od shortName1 is not expected: %s != 0nnnn", shortName1)
	}

	name2 := fixedName + utils.RandString(10) + fixedName
	shortName2, _ := idGenerator.GetId(name2)
	if shortName2 != "1nnnn" {
		t.Fatalf("value od shortName1 is not expected: %s != 1nnnn", shortName2)
	}
}
